package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/google/uuid"
)

// ---------- Data models ----------

// DataPacket represents a captured network request/response pair.
type DataPacket struct {
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     string            `json:"request_body,omitempty"`
	Status          int               `json:"status"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    string            `json:"response_body,omitempty"`
	ResourceType    string            `json:"resource_type"`
	Timestamp       float64           `json:"timestamp"`
}

// DownloadMission tracks a single download lifecycle.
type DownloadMission struct {
	MissionID         string `json:"mission_id"`
	URL               string `json:"url"`
	SuggestedFilename string `json:"suggested_filename"`
	State             string `json:"state"` // pending|in_progress|done|failed|canceled
	SavePath          string `json:"save_path,omitempty"`
	Error             string `json:"error,omitempty"`
}

// RouteEntry records an active request interception route.
type RouteEntry struct {
	ID          string            `json:"id"`
	Pattern     string            `json:"pattern"`
	Action      string            `json:"action"` // abort|add_headers|mock_response|continue
	Headers     map[string]string `json:"headers,omitempty"`
	MockStatus  int               `json:"mock_status,omitempty"`
	MockBody    string            `json:"mock_body,omitempty"`
	MockHeaders map[string]string `json:"mock_headers,omitempty"`
}

// ActionsState tracks the current mouse/keyboard state for the Actions API.
type ActionsState struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Holding bool    `json:"holding"`
}

// BrowserSession holds all state for a single browser automation session.
type BrowserSession struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
	Headless  bool      `json:"headless"`

	browser   *rod.Browser
	pages     map[string]*rod.Page // tabID → Page
	activeTab string

	// Dialog state
	alertText     string
	alertType     string
	autoAlert     bool
	cancelDialogs context.CancelFunc // cancels all dialog handler goroutines

	// Network listener
	networkListener *NetworkListener

	// Download tracking
	downloads   map[string]*DownloadMission
	downloadDir string

	// Routes
	routes       map[string]*RouteEntry
	routeActive  bool
	routeStopCh  chan struct{}
	extraHeaders map[string]string

	// Load mode
	loadMode string // normal|eager|none

	// HTTP dual-mode client
	httpClient *http.Client
	tlsClient  *TLSClient // TLS fingerprint-impersonating client (Phase 3)

	// IFrame context — when non-nil, operations target this frame
	iframeCtx *rod.Page

	// Actions API state
	actionsState ActionsState

	// Browser logs (CDP Log.entryAdded)
	browserLogs       []map[string]interface{}
	browserLogEnabled bool

	// Screencast
	screencastActive bool

	// User agent (real detected)
	realUA string

	// Browser fingerprint for consistent header generation
	fingerprint *BrowserFingerprint

	// Proxy rotation (Phase 5)
	proxyRotator *ProxyRotator

	// Resource blocking (Phase 6)
	resourceBlocker *ResourceBlocker

	// CDP connection mode
	isCDP        bool
	isPersistent bool

	mu sync.RWMutex
}

// activePage returns the current active page, respecting iframe context.
func (s *BrowserSession) activePage() *rod.Page {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.iframeCtx != nil {
		return s.iframeCtx
	}
	if p, ok := s.pages[s.activeTab]; ok {
		return p
	}
	return nil
}

// touch updates LastUsed timestamp.
func (s *BrowserSession) touch() {
	s.mu.Lock()
	s.LastUsed = time.Now().UTC()
	s.mu.Unlock()
}

// ---------- Session Manager ----------

// SessionManager manages browser sessions with a concurrency-safe pool.
type SessionManager struct {
	sessions    map[string]*BrowserSession
	mu          sync.RWMutex
	maxSessions int
	idleTimeout time.Duration
}

// NewSessionManager creates a SessionManager with given limits.
func NewSessionManager(maxSessions int, idleTimeout time.Duration) *SessionManager {
	if maxSessions <= 0 {
		maxSessions = 5
	}
	if idleTimeout <= 0 {
		idleTimeout = 30 * time.Minute
	}
	sm := &SessionManager{
		sessions:    make(map[string]*BrowserSession),
		maxSessions: maxSessions,
		idleTimeout: idleTimeout,
	}
	go sm.cleanupLoop()
	return sm
}

var globalManager *SessionManager
var globalManagerOnce sync.Once

func getManager() *SessionManager {
	globalManagerOnce.Do(func() {
		globalManager = NewSessionManager(5, 30*time.Minute)
	})
	return globalManager
}

// CreateSession creates a new browser session. Supports 3 modes:
// 1. CDP connect (cdpURL set) — attach to existing Chrome
// 2. Persistent (userDataDir set) — reuse profile
// 3. Standard launch — fresh browser
func (sm *SessionManager) CreateSession(ctx context.Context, in *Input) (*BrowserSession, error) {
	sm.mu.Lock()
	if len(sm.sessions) >= sm.maxSessions {
		sm.mu.Unlock()
		return nil, fmt.Errorf("max sessions (%d) reached, close an existing session first", sm.maxSessions)
	}
	sm.mu.Unlock()

	headless := true
	if in.Headless != nil {
		headless = *in.Headless
	}

	var browser *rod.Browser
	var isCDP bool

	if in.CDPURL != "" {
		// Mode 1: CDP connect to existing Chrome
		browser = rod.New().ControlURL(in.CDPURL)
		err := browser.Connect()
		if err != nil {
			return nil, fmt.Errorf("CDP connect to %s failed: %w", in.CDPURL, err)
		}
		isCDP = true
		slog.Info("browser: connected via CDP", slog.String("url", in.CDPURL))
	} else {
		// Mode 2/3: Launch new browser
		l := launcher.New()
		if headless {
			l = l.Headless(true)
		} else {
			l = l.Headless(false)
		}
		if in.Proxy != "" {
			l = l.Proxy(in.Proxy)
		}
		if in.UserDataDir != "" {
			l = l.UserDataDir(in.UserDataDir)
		}
		if in.IgnoreHTTPS {
			l = l.Set("ignore-certificate-errors", "")
		}

		// Anti-detection launcher flags — ported from Scrapling STEALTH_ARGS + DEFAULT_ARGS
		for flag, val := range stealthLauncherFlags {
			l = l.Set(flags.Flag(flag), val)
		}
		for _, flag := range harmfulFlags {
			l = l.Delete(flags.Flag(flag))
		}
		// Conditional stealth flags from input options
		for flag, val := range ConditionalStealthFlags(in.BlockWebRTC, in.HideCanvas, in.DisableWebGL) {
			l = l.Set(flags.Flag(flag), val)
		}

		controlURL, err := l.Launch()
		if err != nil {
			return nil, fmt.Errorf("browser launch failed: %w", err)
		}

		browser = rod.New().ControlURL(controlURL)
		if err := browser.Connect(); err != nil {
			return nil, fmt.Errorf("browser connect failed: %w", err)
		}
		slog.Info("browser: launched", slog.Bool("headless", headless))
	}

	// Create page with stealth
	page, err := stealth.Page(browser)
	if err != nil {
		browser.Close()
		return nil, fmt.Errorf("stealth page creation failed: %w", err)
	}

	// Set viewport
	vw, vh := 1280, 720
	if in.ViewportW > 0 {
		vw = in.ViewportW
	}
	if in.ViewportH > 0 {
		vh = in.ViewportH
	}
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: vw, Height: vh, DeviceScaleFactor: 0, Mobile: false,
	}); err != nil {
		slog.Warn("browser: viewport set failed", slog.Any("err", err))
	}

	// Inject custom anti-detect + console scripts
	_, err = page.EvalOnNewDocument(antiDetectScript)
	if err != nil {
		slog.Warn("browser: anti-detect inject failed", slog.Any("err", err))
	}
	_, err = page.EvalOnNewDocument(consoleInitScript)
	if err != nil {
		slog.Warn("browser: console init inject failed", slog.Any("err", err))
	}

	// Detect and clean User-Agent
	ua := detectAndCleanUA(page)

	// Generate browser fingerprint for consistent identity
	fp := GenerateFingerprint("chrome")
	// Override UA with fingerprint's UA for consistency
	if fp.UserAgent != "" {
		_ = proto.NetworkSetUserAgentOverride{UserAgent: fp.UserAgent}.Call(page)
		ua = fp.UserAgent
	}
	// Inject navigator property overrides (platform, userAgentData, etc.)
	_, _ = page.EvalOnNewDocument(fp.NavigatorOverrideJS())

	sessionID := uuid.New().String()[:8]
	tabID := uuid.New().String()[:8]

	jar, _ := cookiejar.New(nil)

	dialogCtx, dialogCancel := context.WithCancel(context.Background())

	// Create TLS client if TLS impersonation is requested
	var tlsClient *TLSClient
	if in.TLSImpersonate != "" {
		profile := ResolveTLSProfile(in.TLSImpersonate)
		tlsClient = NewTLSClient(profile, jar, 30*time.Second)
		slog.Info("browser: TLS impersonation enabled", slog.String("profile", string(profile)))
	}

	// Create proxy rotator if multiple proxies provided (Phase 5)
	var proxyRot *ProxyRotator
	if len(in.Proxies) > 0 {
		var proxyCfgs []ProxyConfig
		for _, p := range in.Proxies {
			cfg, err := ParseProxyString(p)
			if err != nil {
				slog.Warn("browser: invalid proxy, skipping", slog.String("proxy", p), slog.Any("err", err))
				continue
			}
			proxyCfgs = append(proxyCfgs, cfg)
		}
		if len(proxyCfgs) > 0 {
			strategy := CyclicRotation
			if strings.EqualFold(in.ProxyStrategy, "random") {
				strategy = RandomRotation
			}
			proxyRot, _ = NewProxyRotator(proxyCfgs, strategy)
			slog.Info("browser: proxy rotation enabled", slog.Int("count", len(proxyCfgs)), slog.String("strategy", in.ProxyStrategy))
		}
	}

	// Create resource blocker if any blocking options set (Phase 6)
	var resBlocker *ResourceBlocker
	if in.DisableResources || len(in.BlockedDomains) > 0 || in.BlockAds {
		resBlocker = NewResourceBlocker(in.DisableResources, in.BlockedDomains, in.BlockAds)
		slog.Info("browser: resource blocking enabled",
			slog.Bool("resources", in.DisableResources),
			slog.Bool("ads", in.BlockAds),
			slog.Int("customDomains", len(in.BlockedDomains)))
	}

	session := &BrowserSession{
		ID:              sessionID,
		CreatedAt:       time.Now().UTC(),
		LastUsed:        time.Now().UTC(),
		Headless:        headless,
		browser:         browser,
		pages:           map[string]*rod.Page{tabID: page},
		activeTab:       tabID,
		autoAlert:       true,
		cancelDialogs:   dialogCancel,
		downloads:       make(map[string]*DownloadMission),
		routes:          make(map[string]*RouteEntry),
		extraHeaders:    make(map[string]string),
		loadMode:        "normal",
		httpClient:      &http.Client{Jar: jar, Timeout: 30 * time.Second},
		tlsClient:       tlsClient,
		actionsState:    ActionsState{},
		realUA:          ua,
		fingerprint:     &fp,
		proxyRotator:    proxyRot,
		resourceBlocker: resBlocker,
		isCDP:           isCDP,
		isPersistent:    in.UserDataDir != "",
	}

	// Set up dialog handler (blocks in goroutine until context canceled)
	go session.handleDialogs(dialogCtx, page)

	sm.mu.Lock()
	sm.sessions[sessionID] = session
	sm.mu.Unlock()

	slog.Info("browser: session created",
		slog.String("id", sessionID),
		slog.String("tab", tabID),
		slog.Bool("headless", headless))

	return session, nil
}

// GetSession retrieves a session by ID, updating its last-used time.
func (sm *SessionManager) GetSession(sessionID string) (*BrowserSession, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sessionID == "" {
		// Auto-select the most recent session
		var latest *BrowserSession
		for _, s := range sm.sessions {
			if latest == nil || s.LastUsed.After(latest.LastUsed) {
				latest = s
			}
		}
		if latest == nil {
			return nil, fmt.Errorf("no active browser sessions; call create_session first")
		}
		latest.touch()
		return latest, nil
	}

	s, ok := sm.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	s.touch()
	return s, nil
}

// CloseSession closes and removes a session, releasing all resources.
func (sm *SessionManager) CloseSession(sessionID string) error {
	sm.mu.Lock()
	s, ok := sm.sessions[sessionID]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("session %q not found", sessionID)
	}
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()

	return s.close()
}

// ListSessions returns info about all active sessions.
func (sm *SessionManager) ListSessions() []map[string]interface{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var list []map[string]interface{}
	for _, s := range sm.sessions {
		s.mu.RLock()
		idle := time.Since(s.LastUsed).Seconds()
		list = append(list, map[string]interface{}{
			"session_id":   s.ID,
			"created_at":   s.CreatedAt.Format(time.RFC3339),
			"last_used":    s.LastUsed.Format(time.RFC3339),
			"headless":     s.Headless,
			"idle_seconds": int(idle),
			"active_tab":   s.activeTab,
			"tab_count":    len(s.pages),
		})
		s.mu.RUnlock()
	}
	return list
}

// close releases all resources for a session.
func (s *BrowserSession) close() error {
	// Cancel dialog handler goroutines first (non-blocking)
	if s.cancelDialogs != nil {
		s.cancelDialogs()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.networkListener != nil {
		s.networkListener.Stop()
	}

	if s.routeActive && s.routeStopCh != nil {
		close(s.routeStopCh)
		s.routeStopCh = nil
		s.routeActive = false
	}

	if s.httpClient != nil {
		s.httpClient.CloseIdleConnections()
	}
	if s.tlsClient != nil {
		s.tlsClient.CloseIdleConnections()
	}

	for _, p := range s.pages {
		_ = p.Close()
	}

	if s.browser != nil {
		_ = s.browser.Close()
	}

	slog.Info("browser: session closed", slog.String("id", s.ID))
	return nil
}

// handleDialogs sets up dialog auto-handling for a page.
// The goroutine blocks on wait() until ctx is canceled (session close).
func (s *BrowserSession) handleDialogs(ctx context.Context, page *rod.Page) {
	wait := page.EachEvent(func(e *proto.PageJavascriptDialogOpening) bool {
		s.mu.Lock()
		s.alertText = e.Message
		s.alertType = string(e.Type)
		auto := s.autoAlert
		s.mu.Unlock()

		if auto {
			accept := true
			if e.Type == proto.PageDialogTypePrompt {
				_ = proto.PageHandleJavaScriptDialog{Accept: accept, PromptText: ""}.Call(page)
			} else {
				_ = proto.PageHandleJavaScriptDialog{Accept: accept}.Call(page)
			}
		}
		// return false to keep listening for more events
		return false
	})

	// Block until context is canceled or wait completes
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return
	case <-done:
		return
	}
}

// cleanupLoop periodically removes idle sessions.
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		sm.cleanupIdle()
	}
}

func (sm *SessionManager) cleanupIdle() {
	// Phase 1: collect IDs to close under read lock
	sm.mu.RLock()
	now := time.Now().UTC()
	var toClose []string
	for id, s := range sm.sessions {
		s.mu.RLock()
		idle := now.Sub(s.LastUsed)
		s.mu.RUnlock()
		if idle > sm.idleTimeout {
			toClose = append(toClose, id)
		}
	}
	sm.mu.RUnlock()

	if len(toClose) == 0 {
		return
	}

	// Phase 2: remove from map under write lock
	sm.mu.Lock()
	var sessions []*BrowserSession
	for _, id := range toClose {
		if s, ok := sm.sessions[id]; ok {
			slog.Info("browser: cleaning up idle session", slog.String("id", id))
			delete(sm.sessions, id)
			sessions = append(sessions, s)
		}
	}
	sm.mu.Unlock()

	// Phase 3: close sessions outside of any lock
	for _, s := range sessions {
		_ = s.close()
	}
}

// detectAndCleanUA detects the browser's User-Agent and removes headless markers.
func detectAndCleanUA(page *rod.Page) string {
	res, err := page.Eval(`() => navigator.userAgent`)
	if err != nil {
		return ""
	}
	ua := res.Value.Str()

	// Clean headless markers
	if strings.Contains(ua, "HeadlessChrome") {
		cleaned := strings.Replace(ua, "HeadlessChrome", "Chrome", 1)
		// Override via CDP
		_ = proto.NetworkSetUserAgentOverride{UserAgent: cleaned}.Call(page)
		// Also inject via JS for consistency
		js := fmt.Sprintf(`Object.defineProperty(navigator,'userAgent',{get:()=>%q})`, cleaned)
		_, _ = page.EvalOnNewDocument(js)
		return cleaned
	}
	return ua
}

// syncCookiesToHTTP copies browser cookies to the session's http.Client cookie jar.
func (s *BrowserSession) syncCookiesToHTTP(page *rod.Page) error {
	cookies, err := page.Cookies(nil)
	if err != nil {
		return err
	}

	jar := s.httpClient.Jar
	if jar == nil {
		return fmt.Errorf("http client has no cookie jar")
	}

	for _, c := range cookies {
		domain := c.Domain
		if domain == "" {
			continue
		}
		scheme := "https"
		if !c.Secure {
			scheme = "http"
		}
		u, _ := url.Parse(fmt.Sprintf("%s://%s%s", scheme, strings.TrimPrefix(domain, "."), c.Path))
		if u == nil {
			continue
		}
		jar.SetCookies(u, []*http.Cookie{{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		}})
	}
	return nil
}

// resultJSON marshals v to a JSON string, falling back to fmt.Sprintf on error.
func resultJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
