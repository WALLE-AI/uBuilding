package browser

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// NetworkListener captures network request/response pairs matching configurable
// URL patterns, HTTP methods, and resource types via CDP Network domain events.
type NetworkListener struct {
	page    *rod.Page
	targets []string
	isRegex bool
	methods []string
	types   []string

	packets []*DataPacket
	pending map[string]*pendingReq // requestId → partial data
	mu      sync.Mutex
	active  bool
	stopCh  chan struct{} // closed to signal event goroutine to exit

	maxPackets int
}

type pendingReq struct {
	url            string
	method         string
	requestHeaders map[string]string
	requestBody    string
	resourceType   string
	timestamp      float64
}

// NewNetworkListener creates a listener attached to the given page.
func NewNetworkListener(page *rod.Page) *NetworkListener {
	return &NetworkListener{
		page:       page,
		pending:    make(map[string]*pendingReq),
		maxPackets: 500,
	}
}

// Start begins capturing network traffic matching the given filters.
func (nl *NetworkListener) Start(targets []string, isRegex bool, methods []string, types []string) {
	nl.mu.Lock()
	defer nl.mu.Unlock()

	nl.targets = targets
	nl.isRegex = isRegex
	nl.methods = methods
	nl.types = types
	nl.packets = nil
	nl.pending = make(map[string]*pendingReq)
	nl.active = true
	nl.stopCh = make(chan struct{})

	// Enable CDP Network domain
	_ = proto.NetworkEnable{}.Call(nl.page)

	// Set up event listeners — wait() blocks, so run in goroutine
	wait := nl.page.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			nl.onRequest(e)
		},
		func(e *proto.NetworkResponseReceived) {
			nl.onResponse(e)
		},
	)

	stopCh := nl.stopCh
	go func() {
		done := make(chan struct{})
		go func() {
			wait()
			close(done)
		}()
		select {
		case <-stopCh:
			return
		case <-done:
			return
		}
	}()
}

// Stop disables network capture.
func (nl *NetworkListener) Stop() {
	nl.mu.Lock()
	defer nl.mu.Unlock()

	if !nl.active {
		return
	}
	nl.active = false
	if nl.stopCh != nil {
		close(nl.stopCh)
		nl.stopCh = nil
	}
}

// Clear empties the captured packet buffer.
func (nl *NetworkListener) Clear() {
	nl.mu.Lock()
	defer nl.mu.Unlock()
	nl.packets = nil
}

// GetAll returns all captured packets (up to maxCount, 0=all).
func (nl *NetworkListener) GetAll(maxCount int) []*DataPacket {
	nl.mu.Lock()
	defer nl.mu.Unlock()

	if maxCount <= 0 || maxCount > len(nl.packets) {
		cp := make([]*DataPacket, len(nl.packets))
		copy(cp, nl.packets)
		return cp
	}
	cp := make([]*DataPacket, maxCount)
	copy(cp, nl.packets[len(nl.packets)-maxCount:])
	return cp
}

// WaitForCount blocks until at least count packets are captured or timeout.
func (nl *NetworkListener) WaitForCount(count int, timeout time.Duration) []*DataPacket {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		nl.mu.Lock()
		if len(nl.packets) >= count {
			cp := make([]*DataPacket, len(nl.packets))
			copy(cp, nl.packets)
			nl.mu.Unlock()
			return cp
		}
		nl.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	return nl.GetAll(0)
}

// WaitSilent waits until no new packets arrive for the given duration.
func (nl *NetworkListener) WaitSilent(silenceDuration time.Duration, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	lastCount := -1
	silenceStart := time.Now()

	for time.Now().Before(deadline) {
		nl.mu.Lock()
		currentCount := len(nl.packets)
		nl.mu.Unlock()

		if currentCount != lastCount {
			lastCount = currentCount
			silenceStart = time.Now()
		} else if time.Since(silenceStart) >= silenceDuration {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Count returns the number of captured packets.
func (nl *NetworkListener) Count() int {
	nl.mu.Lock()
	defer nl.mu.Unlock()
	return len(nl.packets)
}

// IsActive returns whether the listener is currently capturing.
func (nl *NetworkListener) IsActive() bool {
	nl.mu.Lock()
	defer nl.mu.Unlock()
	return nl.active
}

// onRequest handles CDP NetworkRequestWillBeSent events.
func (nl *NetworkListener) onRequest(e *proto.NetworkRequestWillBeSent) {
	nl.mu.Lock()
	defer nl.mu.Unlock()

	if !nl.active {
		return
	}

	reqURL := e.Request.URL
	if !nl.matchURL(reqURL) {
		return
	}
	method := e.Request.Method
	if !nl.matchMethod(method) {
		return
	}
	resType := string(e.Type)
	if !nl.matchType(resType) {
		return
	}

	headers := make(map[string]string)
	for k, v := range e.Request.Headers {
		headers[k] = v.Str()
	}

	var body string
	if e.Request.PostData != "" {
		body = e.Request.PostData
		if len(body) > 2000 {
			body = body[:2000]
		}
	}

	nl.pending[string(e.RequestID)] = &pendingReq{
		url:            reqURL,
		method:         method,
		requestHeaders: headers,
		requestBody:    body,
		resourceType:   resType,
		timestamp:      float64(time.Now().UnixMilli()) / 1000.0,
	}
}

// onResponse handles CDP NetworkResponseReceived events.
func (nl *NetworkListener) onResponse(e *proto.NetworkResponseReceived) {
	nl.mu.Lock()
	defer nl.mu.Unlock()

	if !nl.active {
		return
	}

	reqID := string(e.RequestID)
	pend, ok := nl.pending[reqID]
	if !ok {
		return
	}
	delete(nl.pending, reqID)

	if len(nl.packets) >= nl.maxPackets {
		return
	}

	respHeaders := make(map[string]string)
	for k, v := range e.Response.Headers {
		respHeaders[k] = v.Str()
	}

	// Try to get response body (best-effort, may fail for streaming responses)
	var respBody string
	bodyRes, err := proto.NetworkGetResponseBody{RequestID: e.RequestID}.Call(nl.page)
	if err == nil && bodyRes != nil {
		respBody = bodyRes.Body
		if len(respBody) > 2000 {
			respBody = respBody[:2000]
		}
	}

	nl.packets = append(nl.packets, &DataPacket{
		URL:             pend.url,
		Method:          pend.method,
		RequestHeaders:  pend.requestHeaders,
		RequestBody:     pend.requestBody,
		Status:          e.Response.Status,
		ResponseHeaders: respHeaders,
		ResponseBody:    respBody,
		ResourceType:    pend.resourceType,
		Timestamp:       pend.timestamp,
	})
}

// matchURL checks if a URL matches any of the configured target patterns.
func (nl *NetworkListener) matchURL(u string) bool {
	if len(nl.targets) == 0 {
		return true
	}
	for _, t := range nl.targets {
		if nl.isRegex {
			if matched, _ := regexp.MatchString(t, u); matched {
				return true
			}
		} else {
			if strings.Contains(u, t) {
				return true
			}
		}
	}
	return false
}

// matchMethod checks if a method matches the configured methods filter.
func (nl *NetworkListener) matchMethod(m string) bool {
	if len(nl.methods) == 0 {
		return true
	}
	m = strings.ToUpper(m)
	for _, method := range nl.methods {
		if strings.ToUpper(method) == m {
			return true
		}
	}
	return false
}

// matchType checks if a resource type matches the configured types filter.
func (nl *NetworkListener) matchType(t string) bool {
	if len(nl.types) == 0 {
		return true
	}
	t = strings.ToLower(t)
	for _, typ := range nl.types {
		if strings.ToLower(typ) == t {
			return true
		}
	}
	return false
}
