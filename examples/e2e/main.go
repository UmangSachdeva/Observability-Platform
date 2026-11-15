// Command e2e checks the whole integration against a running API:
// it signs up a user, creates a project and an API key, starts
// examples/demo-service with that key, sends traffic that makes the demo fail
// in known ways, and then uses the search API to confirm every event arrived
// in the right project.
//
// Run it from the repository root while the API is running:
//
//	go run ./examples/e2e -api http://localhost:8080
//
// By default it creates a throwaway user. To see the events in your own
// account (for example in the frontend), log in as yourself instead:
//
//	go run ./examples/e2e -login you@example.com -password '...' [-account <id>] [-project <id>]
//
// Without -project it creates a new "demo-shop-…" project in that account.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type errorEvent struct {
	TraceID         string            `json:"trace_id"`
	OccurrenceCount int               `json:"occurrence_count"`
	ID              string            `json:"id"`
	ProjectID       string            `json:"project_id"`
	Level           string            `json:"level"`
	Service         string            `json:"service"`
	Message         string            `json:"message"`
	StackTrace      string            `json:"stack_trace"`
	Attributes      map[string]string `json:"attributes"`
}

type traceSpan struct {
	Name       string `json:"name"`
	Depth      int    `json:"depth"`
	StatusCode string `json:"status_code"`
}

type traceDetail struct {
	Spans  []traceSpan  `json:"spans"`
	Errors []errorEvent `json:"errors"`
}

func hasSpan(spans []traceSpan, name string, depth int) bool {
	for _, span := range spans {
		if span.Name == name && span.Depth == depth {
			return true
		}
	}
	return false
}

type searchPage struct {
	Errors     []errorEvent `json:"errors"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor"`
}

type runner struct {
	api         string
	environment string
	client      *http.Client
	token       string
	failures    int
}

func main() {
	api := flag.String("api", "http://localhost:8080", "Observability Platform API base URL")
	demoAddr := flag.String("demo-addr", "127.0.0.1:9090", "address for the demo service")
	login := flag.String("login", "", "log in as this username or email instead of creating a new user")
	password := flag.String("password", "", "password for -login")
	accountFlag := flag.String("account", "", "account ID to use with -login (default: the account login returns)")
	projectFlag := flag.String("project", "", "existing project ID to send events to (default: create a new project)")
	flag.Parse()
	r := &runner{api: strings.TrimRight(*api, "/"), client: &http.Client{Timeout: 10 * time.Second}}

	section("1. Setup through the platform API")
	if status, _ := r.call("GET", "/healthz", nil, nil); status != http.StatusOK {
		fatal("API is not reachable at %s (status %d). Start it first.", r.api, status)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 36)
	// Each run tags its events with its own environment, so checks only see
	// this run's events even in a project that already has older ones.
	r.environment = "e2e-" + suffix
	var session struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	}
	if *login != "" {
		r.mustCall("POST", "/v1/auth/login", map[string]string{"login": *login, "password": *password}, &session, http.StatusOK)
		pass("logged in as %s", *login)
	} else {
		r.mustCall("POST", "/v1/auth/signup", map[string]string{
			"username": "e2e_" + suffix, "email": "e2e_" + suffix + "@example.com", "password": "e2e-long-password",
		}, &session, http.StatusCreated)
		pass("signed up throwaway user e2e_%s (password e2e-long-password)", suffix)
	}
	r.token = session.AccessToken
	account := session.AccountID
	if *accountFlag != "" {
		account = *accountFlag
	}
	if account == "" {
		fatal("this user has no account; pass -account <id>")
	}
	pass("using account %s", account)

	var project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if *projectFlag != "" {
		project.ID = *projectFlag
		pass("using existing project %s", project.ID)
	} else {
		r.mustCall("POST", "/v1/accounts/"+account+"/projects", map[string]string{"name": "demo-shop-" + suffix}, &project, http.StatusCreated)
		pass("created project %s (%s)", project.Name, project.ID)
	}

	var key struct {
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
		ProjectID string `json:"project_id"`
	}
	r.mustCall("POST", "/v1/accounts/"+account+"/projects/"+project.ID+"/keys", map[string]string{"client_name": "demo-shop"}, &key, http.StatusCreated)
	r.check(key.ProjectID == project.ID, "API key belongs to the project (%s)", key.AccessKey[:10]+"…")

	section("2. Start the demo service with that key")
	demo, demoURL := startDemo(r.api, key.AccessKey, key.SecretKey, r.environment, *demoAddr)
	pass("demo service up at %s", demoURL)

	section("3. Send traffic to the demo service")
	expect := func(method, path string, want int) {
		request, _ := http.NewRequest(method, demoURL+path, nil)
		response, err := r.client.Do(request)
		if err != nil {
			r.check(false, "%s %s: %v", method, path, err)
			return
		}
		response.Body.Close()
		r.check(response.StatusCode == want, "%s %-22s → %d", method, path, response.StatusCode)
	}
	expect("POST", "/checkout?amount=50", http.StatusOK)
	for range 3 {
		expect("POST", "/checkout?amount=5000", http.StatusPaymentRequired)
	}
	expect("GET", "/orders/42", http.StatusOK)
	expect("GET", "/orders/abc", http.StatusBadRequest)
	expect("GET", "/inventory", http.StatusGatewayTimeout)
	expect("GET", "/panic", http.StatusInternalServerError)
	expect("GET", "/healthz", http.StatusOK) // still serving after the panic

	stopDemo(demo) // the demo flushes queued events on shutdown
	// Only look at events from this run; an existing project may hold older ones.
	pass("demo service stopped and flushed its events")

	section("4. Verify through the search API")
	projectPath := "/v1/accounts/" + account + "/projects/" + project.ID + "/errors"
	// 6 failures happened: 3 identical declined checkouts, a bad order id, an
	// inventory timeout and a panic. Identical errors are stored once with a
	// count, so that is 4 distinct errors with 6 occurrences between them.
	const wantTotal, wantOccurrences = 4, 6
	var all searchPage
	occurrences := 0
	deadline := time.Now().Add(15 * time.Second)
	for {
		all = r.search(projectPath, url.Values{"limit": {"500"}})
		occurrences = 0
		for _, event := range all.Errors {
			occurrences += event.OccurrenceCount
		}
		if occurrences >= wantOccurrences || time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	r.check(len(all.Errors) == wantTotal && occurrences == wantOccurrences,
		"project has %d distinct errors with %d occurrences (want %d and %d)", len(all.Errors), occurrences, wantTotal, wantOccurrences)
	allInProject := true
	for _, event := range all.Errors {
		if event.ProjectID != project.ID || event.Service != "demo-shop" {
			allInProject = false
		}
	}
	r.check(allInProject, "every event is stored under the key's project with service demo-shop")

	fatalEvents := r.search(projectPath, url.Values{"level": {"fatal"}}).Errors
	r.check(len(fatalEvents) == 1 && strings.Contains(fatalEvents[0].Message, "nil map") &&
		strings.Contains(fatalEvents[0].StackTrace, "main.(*shop).crash") && fatalEvents[0].Attributes["path"] == "/panic",
		"panic reported as fatal with stack trace and path")

	declined := r.search(projectPath, url.Values{"q": {"DECLINED"}}).Errors
	r.check(len(declined) == 1 && declined[0].Level == "warning" && declined[0].OccurrenceCount == 3,
		"q=DECLINED finds the declined checkout once, counted 3 times (case-insensitive)")
	if len(declined) == 1 {
		var list struct {
			Occurrences []struct {
				TraceID    string            `json:"trace_id"`
				Attributes map[string]string `json:"attributes"`
			} `json:"occurrences"`
			Total int `json:"total"`
		}
		r.call("GET", projectPath+"/"+declined[0].ID+"/occurrences", nil, &list)
		traces := map[string]bool{}
		for _, occurrence := range list.Occurrences {
			if occurrence.Attributes["amount"] == "5000" && occurrence.TraceID != "" {
				traces[occurrence.TraceID] = true
			}
		}
		r.check(list.Total == 3 && len(list.Occurrences) == 3 && len(traces) == 3,
			"its occurrences list all 3 times, each with its own request's trace_id")
	}

	timeouts := r.search(projectPath, url.Values{"attr.dependency": {"inventory"}}).Errors
	r.check(len(timeouts) == 1 && strings.Contains(timeouts[0].Message, "context deadline exceeded"),
		"attr.dependency=inventory finds the timeout with its wrapped cause")

	r.check(len(r.search(projectPath, url.Values{"level": {"error,fatal"}}).Errors) == 3, "level=error,fatal returns 3 events")
	r.check(len(r.search(projectPath, url.Values{"environment": {r.environment}}).Errors) == wantTotal, "environment=%s returns all events", r.environment)

	seen, pages := map[string]bool{}, 0
	cursor := ""
	for {
		params := url.Values{"limit": {"2"}, "sort": {"level"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		page := r.search(projectPath, params)
		pages++
		for _, event := range page.Errors {
			seen[event.ID] = true
		}
		if !page.HasMore || pages > 10 {
			break
		}
		cursor = page.NextCursor
	}
	r.check(len(seen) == wantTotal && pages == 2, "cursor pagination (limit=2, sort=level): %d pages, %d unique events", pages, len(seen))

	global := r.search("/v1/accounts/"+account+"/errors", url.Values{"project_id": {project.ID}, "service": {"demo-shop"}})
	r.check(len(global.Errors) == wantTotal, "account-wide search with project_id finds all %d events", len(global.Errors))

	section("5. Duplicate handling (signed requests sent directly)")
	r.checkDuplicates(key.AccessKey, key.SecretKey, projectPath, "dup-"+suffix)

	section("6. Verify OpenTelemetry traces")
	linked := 0
	for _, event := range all.Errors {
		if event.TraceID != "" {
			linked++
		}
	}
	r.check(linked == wantTotal, "every error carries the trace_id of its latest occurrence (%d/%d)", linked, wantTotal)
	tracesPath := "/v1/accounts/" + account + "/projects/" + project.ID + "/traces"
	var panicTrace traceDetail
	if len(fatalEvents) == 1 && fatalEvents[0].TraceID != "" {
		deadline := time.Now().Add(15 * time.Second)
		for {
			status, _ := r.call("GET", tracesPath+"/"+fatalEvents[0].TraceID, nil, &panicTrace)
			if (status == http.StatusOK && len(panicTrace.Spans) >= 2) || time.Now().After(deadline) {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	r.check(len(panicTrace.Spans) >= 2 && panicTrace.Spans[0].Name == "GET /panic" && panicTrace.Spans[0].StatusCode == "ERROR" &&
		panicTrace.Spans[0].Depth == 0 && hasSpan(panicTrace.Spans, "POST /v1/errors", 1),
		"panic trace: root GET /panic (ERROR) with the event delivery as its child")
	r.check(len(panicTrace.Errors) == 1 && panicTrace.Errors[0].Level == "fatal", "panic trace links back to its fatal error event")
	var traceList struct {
		Traces []struct {
			RootName   string `json:"root_name"`
			ErrorCount int    `json:"error_count"`
		} `json:"traces"`
	}
	r.call("GET", tracesPath+"?limit=200", nil, &traceList)
	names := map[string]int{}
	for _, trace := range traceList.Traces {
		names[trace.RootName] += trace.ErrorCount + 1
	}
	r.check(names["GET /inventory"] > 1 && names["GET /orders/{id}"] > 0 && names["POST /checkout"] > 0,
		"trace list has the demo routes, named by route pattern (GET /orders/{id})")
	var errorTraces struct {
		Traces []struct {
			ErrorCount int `json:"error_count"`
		} `json:"traces"`
	}
	r.call("GET", tracesPath+"?status=error", nil, &errorTraces)
	onlyErrors := len(errorTraces.Traces) > 0
	for _, trace := range errorTraces.Traces {
		onlyErrors = onlyErrors && trace.ErrorCount > 0
	}
	r.check(onlyErrors, "status=error returns only traces with error spans (%d)", len(errorTraces.Traces))

	fmt.Println()
	if r.failures > 0 {
		fmt.Printf("FAILED: %d check(s) failed\n", r.failures)
		os.Exit(1)
	}
	fmt.Println("PASSED: the demo service's events reached the platform and are searchable")
	fmt.Printf("\nSee them in the frontend: account %s, project %s, environment %s\n", account, project.ID, r.environment)
	if *login == "" {
		fmt.Printf("(log in there as e2e_%s / e2e-long-password, or rerun with -login to use your own user)\n", suffix)
	}
}

func (r *runner) call(method, path string, body, out any) (int, []byte) {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, r.api+path, reader)
	if err != nil {
		fatal("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	return response.StatusCode, data
}

func (r *runner) mustCall(method, path string, body, out any, want int) {
	if status, data := r.call(method, path, body, out); status != want {
		fatal("%s %s returned %d (want %d): %s", method, path, status, want, strings.TrimSpace(string(data)))
	}
}

// search scopes every query to this run's environment.
func (r *runner) search(path string, params url.Values) searchPage {
	if params.Get("environment") == "" {
		params.Set("environment", r.environment)
	}
	var page searchPage
	if status, data := r.call("GET", path+"?"+params.Encode(), nil, &page); status != http.StatusOK {
		r.check(false, "GET %s?%s returned %d: %s", path, params.Encode(), status, strings.TrimSpace(string(data)))
	}
	return page
}

func (r *runner) check(ok bool, format string, args ...any) {
	if ok {
		pass(format, args...)
		return
	}
	r.failures++
	fmt.Printf("  FAIL  %s\n", fmt.Sprintf(format, args...))
}

func startDemo(api, accessKey, secretKey, environment, addr string) (*exec.Cmd, string) {
	if _, err := os.Stat("examples/demo-service/main.go"); err != nil {
		fatal("run this from the repository root (examples/demo-service not found)")
	}
	dir, err := os.MkdirTemp("", "obs-e2e-")
	if err != nil {
		fatal("temp dir: %v", err)
	}
	binary := filepath.Join(dir, "demo-service")
	build := exec.Command("go", "build", "-o", binary, "./examples/demo-service")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fatal("build demo service: %v", err)
	}
	demo := exec.Command(binary)
	demo.Env = append(os.Environ(),
		"OBS_ENDPOINT="+api, "OBS_ACCESS_KEY="+accessKey, "OBS_SECRET_KEY="+secretKey,
		"OBS_ENVIRONMENT="+environment, "DEMO_ADDR="+addr)
	demo.Stdout, demo.Stderr = io.Discard, os.Stderr
	if err := demo.Start(); err != nil {
		fatal("start demo service: %v", err)
	}
	demoURL := "http://" + addr
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if response, err := http.Get(demoURL + "/healthz"); err == nil {
			response.Body.Close()
			return demo, demoURL
		}
		select {
		case <-ctx.Done():
			_ = demo.Process.Kill()
			fatal("demo service did not start on %s", addr)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func stopDemo(demo *exec.Cmd) {
	_ = demo.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- demo.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = demo.Process.Kill()
	}
}

func section(title string) { fmt.Printf("\n%s\n", title) }

func pass(format string, args ...any) { fmt.Printf("  ok    %s\n", fmt.Sprintf(format, args...)) }

func fatal(format string, args ...any) {
	fmt.Printf("  FAIL  %s\n", fmt.Sprintf(format, args...))
	os.Exit(1)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

// checkDuplicates sends signed events straight to the API: the same delivery
// twice (a retry or replay), the same error at a new time, and the same
// place with a different message.
func (r *runner) checkDuplicates(accessKey, secretKey, projectPath, environment string) {
	event := func(message string, at time.Time) []byte {
		body, _ := json.Marshal(map[string]any{
			"service": "demo-shop", "environment": environment, "level": "error",
			"message": message, "stack_trace": "main.charge\n\tcheckout.go:42",
			"timestamp": at.UTC().Format(time.RFC3339Nano),
		})
		return body
	}
	at := time.Now().Add(-time.Minute)
	first := r.signedEvent(accessKey, secretKey, event("payment gateway refused the charge", at))
	replay := r.signedEvent(accessKey, secretKey, event("payment gateway refused the charge", at))
	r.check(first.Status == "accepted" && replay.Status == "duplicate" && first.ID == replay.ID,
		"the same delivery sent twice is stored once (second answer: %q)", replay.Status)
	later := r.signedEvent(accessKey, secretKey, event("payment gateway refused the charge", at.Add(30*time.Second)))
	other := r.signedEvent(accessKey, secretKey, event("payment gateway timed out", at))
	r.check(later.Status == "accepted" && later.ID == first.ID, "the same error at a new time joins the same record")
	r.check(other.Status == "accepted" && other.ID != first.ID, "a different message from the same place is a new error")
	errors := r.search(projectPath, url.Values{"environment": {environment}, "q": {"payment gateway"}}).Errors
	counts := map[string]int{}
	for _, item := range errors {
		counts[item.Message] = item.OccurrenceCount
	}
	r.check(len(errors) == 2 && counts["payment gateway refused the charge"] == 2 && counts["payment gateway timed out"] == 1,
		"stored: 2 distinct errors, counted 2 and 1 times")
}

type ingestAnswer struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// signedEvent posts body to /v1/errors with the HMAC headers the API expects.
func (r *runner) signedEvent(accessKey, secretKey string, body []byte) ingestAnswer {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	bodyHash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(timestamp + "\nPOST\n/v1/errors\n" + hex.EncodeToString(bodyHash[:])))
	request, _ := http.NewRequest(http.MethodPost, r.api+"/v1/errors", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Access-Key", accessKey)
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	var answer ingestAnswer
	response, err := r.client.Do(request)
	if err != nil {
		return answer
	}
	defer response.Body.Close()
	_ = json.NewDecoder(response.Body).Decode(&answer)
	return answer
}
