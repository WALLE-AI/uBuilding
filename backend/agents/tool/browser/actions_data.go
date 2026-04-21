package browser

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rod/rod/lib/proto"
)

func (t *BrowserTool) doGetCookies(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	cookies, err := page.Cookies(nil)
	if err != nil {
		return fmt.Sprintf("get_cookies failed: %v", err)
	}
	if len(cookies) == 0 {
		return "No cookies."
	}

	var items []map[string]interface{}
	for _, c := range cookies {
		items = append(items, map[string]interface{}{
			"name":     c.Name,
			"value":    truncStr(c.Value, 100),
			"domain":   c.Domain,
			"path":     c.Path,
			"secure":   c.Secure,
			"httpOnly": c.HTTPOnly,
			"sameSite": c.SameSite,
		})
	}
	return resultJSON(items)
}

func (t *BrowserTool) doSetCookies(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if len(in.Cookies) == 0 {
		return "Error: cookies array is required"
	}

	var cookieParams []*proto.NetworkCookieParam
	for _, c := range in.Cookies {
		cp := &proto.NetworkCookieParam{
			Name:   c.Name,
			Value:  c.Value,
			Domain: c.Domain,
			Path:   c.Path,
		}
		if c.Secure {
			cp.Secure = true
		}
		if c.HTTPOnly {
			cp.HTTPOnly = true
		}
		cookieParams = append(cookieParams, cp)
	}

	err = page.SetCookies(cookieParams)
	if err != nil {
		return fmt.Sprintf("set_cookies failed: %v", err)
	}
	return fmt.Sprintf("Set %d cookie(s).", len(cookieParams))
}

func (t *BrowserTool) doGetStorage(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	storageType := in.StorageType
	if storageType == "" {
		storageType = "local"
	}

	jsObj := "localStorage"
	if storageType == "session" {
		jsObj = "sessionStorage"
	}

	res, err := page.Eval(fmt.Sprintf(`() => {
		let s = %s;
		let data = {};
		for (let i = 0; i < s.length; i++) {
			let k = s.key(i);
			data[k] = s.getItem(k);
		}
		return data;
	}`, jsObj))
	if err != nil {
		return fmt.Sprintf("get_storage failed: %v", err)
	}
	return fmt.Sprintf("%sStorage:\n%s", storageType, res.Value.Raw())
}

func (t *BrowserTool) doSetStorage(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if len(in.StorageData) == 0 {
		return "Error: storage_data is required"
	}
	storageType := in.StorageType
	if storageType == "" {
		storageType = "local"
	}
	jsObj := "localStorage"
	if storageType == "session" {
		jsObj = "sessionStorage"
	}

	for k, v := range in.StorageData {
		_, err := page.Eval(fmt.Sprintf(`() => %s.setItem(%q, %q)`, jsObj, k, v))
		if err != nil {
			return fmt.Sprintf("set_storage failed for key %q: %v", k, err)
		}
	}
	return fmt.Sprintf("Set %d key(s) in %sStorage.", len(in.StorageData), storageType)
}

func (t *BrowserTool) doClearStorage(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	storageType := in.StorageType
	if storageType == "" {
		storageType = "local"
	}
	jsObj := "localStorage"
	if storageType == "session" {
		jsObj = "sessionStorage"
	}
	_, err = page.Eval(fmt.Sprintf(`() => %s.clear()`, jsObj))
	if err != nil {
		return fmt.Sprintf("clear_storage failed: %v", err)
	}
	return fmt.Sprintf("%sStorage cleared.", storageType)
}

func (t *BrowserTool) doGetConsoleLogs(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	res, err := page.Eval(`() => window.__drission_console_logs__ || []`)
	if err != nil {
		return fmt.Sprintf("get_console_logs failed: %v", err)
	}
	raw := res.Value.Raw()
	if raw == nil {
		return "No console logs captured."
	}
	return fmt.Sprintf("Console logs:\n%v", raw)
}

func (t *BrowserTool) doScreenshot(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	fullPage := in.FullPage
	quality := in.Quality
	if quality <= 0 {
		quality = 80
	}

	format := proto.PageCaptureScreenshotFormatPng
	if in.Format == "jpeg" || in.Format == "jpg" {
		format = proto.PageCaptureScreenshotFormatJpeg
	}

	var data []byte
	if fullPage {
		data, err = page.Screenshot(fullPage, &proto.PageCaptureScreenshot{
			Format:  format,
			Quality: &quality,
		})
	} else {
		data, err = page.Screenshot(false, &proto.PageCaptureScreenshot{
			Format:  format,
			Quality: &quality,
		})
	}
	if err != nil {
		return fmt.Sprintf("screenshot failed: %v", err)
	}

	// Save to file if path provided
	if in.ScreenshotPath != "" {
		dir := filepath.Dir(in.ScreenshotPath)
		_ = os.MkdirAll(dir, 0o755)
		err = os.WriteFile(in.ScreenshotPath, data, 0o644)
		if err != nil {
			return fmt.Sprintf("screenshot captured but save failed: %v", err)
		}
		return fmt.Sprintf("Screenshot saved: %s (%d bytes)", in.ScreenshotPath, len(data))
	}

	// Return as base64
	b64 := base64.StdEncoding.EncodeToString(data)
	if len(b64) > 100000 {
		b64 = b64[:100000] + "... (truncated)"
	}
	return fmt.Sprintf("Screenshot captured (%d bytes).\nbase64: %s", len(data), b64)
}

func (t *BrowserTool) doScreenshotElement(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}

	data, err := el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
	if err != nil {
		return fmt.Sprintf("screenshot_element failed: %v", err)
	}

	if in.ScreenshotPath != "" {
		dir := filepath.Dir(in.ScreenshotPath)
		_ = os.MkdirAll(dir, 0o755)
		err = os.WriteFile(in.ScreenshotPath, data, 0o644)
		if err != nil {
			return fmt.Sprintf("screenshot captured but save failed: %v", err)
		}
		return fmt.Sprintf("Element screenshot saved: %s (%d bytes)", in.ScreenshotPath, len(data))
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	if len(b64) > 100000 {
		b64 = b64[:100000] + "... (truncated)"
	}
	return fmt.Sprintf("Element screenshot captured (%d bytes).\nbase64: %s", len(data), b64)
}

func (t *BrowserTool) doPDF(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	req := &proto.PagePrintToPDF{}
	data, err := page.PDF(req)
	if err != nil {
		return fmt.Sprintf("pdf failed: %v", err)
	}

	savePath := in.SavePath
	if savePath == "" {
		savePath = in.ScreenshotPath
	}
	if savePath != "" {
		dir := filepath.Dir(savePath)
		_ = os.MkdirAll(dir, 0o755)
		buf, readErr := io.ReadAll(data)
		if readErr != nil {
			return fmt.Sprintf("PDF read failed: %v", readErr)
		}
		err = os.WriteFile(savePath, buf, 0o644)
		if err != nil {
			return fmt.Sprintf("PDF generated but save failed: %v", err)
		}
		return fmt.Sprintf("PDF saved: %s (%d bytes)", savePath, len(buf))
	}
	return "PDF generated. Provide save_path to write to disk."
}

func (t *BrowserTool) doSetupDownload(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	dir := in.DownloadDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "browser_downloads")
	}
	_ = os.MkdirAll(dir, 0o755)

	err = proto.BrowserSetDownloadBehavior{
		Behavior:     proto.BrowserSetDownloadBehaviorBehaviorAllowAndName,
		DownloadPath: dir,
	}.Call(page)
	if err != nil {
		return fmt.Sprintf("setup_download failed: %v", err)
	}
	return fmt.Sprintf("Download directory set: %s", dir)
}

func (t *BrowserTool) doListDownloads(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.downloads) == 0 {
		return "No downloads tracked."
	}
	return resultJSON(s.downloads)
}

func (t *BrowserTool) doSnapshot(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	// Get page title, URL, and a structural snapshot
	info := safeInfo(page)
	res, err := page.Eval(`() => {
		function snap(el, depth) {
			if (depth > 5) return '';
			let tag = el.tagName ? el.tagName.toLowerCase() : '';
			let txt = (el.innerText || '').trim();
			if (txt.length > 100) txt = txt.substring(0, 100) + '...';
			let children = [];
			for (let c of (el.children || [])) {
				if (c.tagName) children.push(snap(c, depth + 1));
			}
			let childStr = children.filter(x => x).join('\n');
			let indent = '  '.repeat(depth);
			if (!tag) return '';
			let id = el.id ? '#'+el.id : '';
			let cls = el.className && typeof el.className === 'string' ? '.'+el.className.split(' ')[0] : '';
			let line = indent + '<' + tag + id + cls + '>';
			if (txt && !childStr) line += ' ' + txt;
			if (childStr) line += '\n' + childStr;
			return line;
		}
		return snap(document.body, 0);
	}`)

	var lines []string
	lines = append(lines, fmt.Sprintf("Page Snapshot"))
	lines = append(lines, fmt.Sprintf("  URL: %s", info.URL))
	lines = append(lines, fmt.Sprintf("  Title: %s", info.Title))
	lines = append(lines, "---")
	if err == nil {
		snapshot := res.Value.Str()
		if len(snapshot) > 5000 {
			snapshot = snapshot[:5000] + "\n... (truncated)"
		}
		lines = append(lines, snapshot)
	} else {
		lines = append(lines, fmt.Sprintf("Snapshot error: %v", err))
	}
	return strings.Join(lines, "\n")
}
