package browser

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// doSmartClick implements the 3-level fallback click strategy:
// Level 1: wait for element + animation stable
// Level 2: scroll into view if not visible
// Level 3: JS click fallback if native click fails
func (t *BrowserTool) doSmartClick(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found for click: %v\nLocator: %s", err, in.Locator)
	}

	// Level 1: Wait for element to be stable
	_ = el.WaitStable(300 * time.Millisecond)

	// Level 2: Scroll into view if needed
	err = el.ScrollIntoView()
	if err != nil {
		// Continue anyway
	}

	// Try native click
	err = el.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		// Level 3: JS click fallback
		_, jsErr := el.Eval(`(el) => el.click()`)
		if jsErr != nil {
			return fmt.Sprintf("smart_click failed (all 3 levels):\n  native: %v\n  JS: %v", err, jsErr)
		}
		return fmt.Sprintf("Clicked (JS fallback). Locator: %s", in.Locator)
	}
	return fmt.Sprintf("Clicked successfully. Locator: %s", in.Locator)
}

func (t *BrowserTool) doHover(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}
	err = el.Hover()
	if err != nil {
		return fmt.Sprintf("hover failed: %v", err)
	}
	return fmt.Sprintf("Hovered on element. Locator: %s", in.Locator)
}

func (t *BrowserTool) doDoubleClick(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}
	err = el.Click(proto.InputMouseButtonLeft, 2)
	if err != nil {
		return fmt.Sprintf("double_click failed: %v", err)
	}
	return fmt.Sprintf("Double-clicked. Locator: %s", in.Locator)
}

func (t *BrowserTool) doRightClick(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}
	err = el.Click(proto.InputMouseButtonRight, 1)
	if err != nil {
		return fmt.Sprintf("right_click failed: %v", err)
	}
	return fmt.Sprintf("Right-clicked. Locator: %s", in.Locator)
}

func (t *BrowserTool) doDragDrop(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.DragToLocator == "" {
		return "Error: drag_to_locator is required"
	}
	src, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Source element not found: %v", err)
	}

	destResolved := Resolve(in.DragToLocator)
	var dst *rod.Element
	switch destResolved.Strategy {
	case StrategyXPath:
		dst, err = page.ElementX(destResolved.Value)
	default:
		dst, err = page.Element(destResolved.Value)
	}
	if err != nil {
		return fmt.Sprintf("Destination element not found: %v", err)
	}

	// Get positions
	srcShape, _ := src.Shape()
	dstShape, _ := dst.Shape()
	if srcShape == nil || dstShape == nil {
		return "Error: could not determine element positions"
	}
	srcBox := srcShape.Box()
	dstBox := dstShape.Box()

	srcX := srcBox.X + srcBox.Width/2
	srcY := srcBox.Y + srcBox.Height/2
	dstX := dstBox.X + dstBox.Width/2
	dstY := dstBox.Y + dstBox.Height/2

	mouse := page.Mouse
	err = mouse.MoveTo(proto.NewPoint(srcX, srcY))
	if err != nil {
		return fmt.Sprintf("drag: move to source failed: %v", err)
	}
	err = mouse.Down(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Sprintf("drag: mouse down failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	err = mouse.MoveTo(proto.NewPoint(dstX, dstY))
	if err != nil {
		return fmt.Sprintf("drag: move to dest failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	err = mouse.Up(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Sprintf("drag: mouse up failed: %v", err)
	}

	return "Drag and drop completed."
}

func (t *BrowserTool) doSelectOption(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Select element not found: %v", err)
	}
	if in.Value == "" && in.Text == "" {
		return "Error: value or text is required for select_option"
	}

	selectVal := in.Value
	if selectVal == "" {
		selectVal = in.Text
	}

	err = el.Select([]string{selectVal}, true, rod.SelectorTypeText)
	if err != nil {
		// Fallback: try by value
		_, jsErr := el.Eval(fmt.Sprintf(`(el) => {
			for (let opt of el.options) {
				if (opt.value === %q || opt.text === %q) {
					opt.selected = true;
					el.dispatchEvent(new Event('change'));
					return true;
				}
			}
			return false;
		}`, selectVal, selectVal))
		if jsErr != nil {
			return fmt.Sprintf("select_option failed: %v", err)
		}
	}
	return fmt.Sprintf("Selected option: %q", selectVal)
}

func (t *BrowserTool) doUploadFile(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("File input not found: %v", err)
	}
	if len(in.FilePaths) == 0 {
		return "Error: file_paths is required"
	}
	err = el.SetFiles(in.FilePaths)
	if err != nil {
		return fmt.Sprintf("upload_file failed: %v", err)
	}
	return fmt.Sprintf("Uploaded %d file(s).", len(in.FilePaths))
}

func (t *BrowserTool) doExecuteJS(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.Script == "" {
		return "Error: script is required"
	}
	res, err := page.Eval(in.Script)
	if err != nil {
		return fmt.Sprintf("JS execution error: %v", err)
	}
	return fmt.Sprintf("JS result: %s", res.Value.Raw())
}

func (t *BrowserTool) doKeyPress(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	key := in.Key
	if key == "" {
		return "Error: key is required (e.g. Enter, Tab, Escape)"
	}

	k := mapKeyName(key)
	err = page.Keyboard.Press(k)
	if err != nil {
		return fmt.Sprintf("key_press %q failed: %v", key, err)
	}
	return fmt.Sprintf("Pressed key: %s", key)
}

func (t *BrowserTool) doClearInput(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}
	err = el.SelectAllText()
	if err == nil {
		err = page.Keyboard.Press(input.Backspace)
	}
	if err != nil {
		// JS fallback
		_, _ = el.Eval(`(el) => { el.value = ''; el.dispatchEvent(new Event('input')); }`)
	}
	return "Input cleared."
}

func (t *BrowserTool) doInput(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}
	text := in.Text
	if text == "" {
		text = in.Value
	}
	err = el.Input(text)
	if err != nil {
		// Fallback: SelectAll + type
		_ = el.SelectAllText()
		err = el.Input(text)
		if err != nil {
			return fmt.Sprintf("input failed: %v", err)
		}
	}
	return fmt.Sprintf("Input text: %q", truncStr(text, 50))
}

func (t *BrowserTool) doScroll(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	direction := in.ScrollDirection
	if direction == "" {
		direction = "down"
	}
	amount := in.ScrollAmount
	if amount <= 0 {
		amount = 300
	}

	// If locator is provided, scroll element into view
	if in.Locator != "" {
		el, elErr := t.resolveElement(page, in)
		if elErr != nil {
			return fmt.Sprintf("Element not found for scroll: %v", elErr)
		}
		if direction == "into_view" {
			err = el.ScrollIntoView()
			if err != nil {
				return fmt.Sprintf("scroll into view failed: %v", err)
			}
			return "Scrolled element into view."
		}
	}

	var js string
	switch direction {
	case "up":
		js = fmt.Sprintf("window.scrollBy(0, -%d)", amount)
	case "down":
		js = fmt.Sprintf("window.scrollBy(0, %d)", amount)
	case "left":
		js = fmt.Sprintf("window.scrollBy(-%d, 0)", amount)
	case "right":
		js = fmt.Sprintf("window.scrollBy(%d, 0)", amount)
	case "top":
		js = "window.scrollTo(0, 0)"
	case "bottom":
		js = "window.scrollTo(0, document.body.scrollHeight)"
	case "into_view":
		return "Error: into_view requires a locator"
	default:
		return fmt.Sprintf("Unknown scroll direction: %q", direction)
	}

	_, err = page.Eval(js)
	if err != nil {
		return fmt.Sprintf("scroll failed: %v", err)
	}
	return fmt.Sprintf("Scrolled %s %dpx.", direction, amount)
}

func (t *BrowserTool) doGetHTML(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.Locator != "" {
		el, elErr := t.resolveElement(page, in)
		if elErr != nil {
			return fmt.Sprintf("Element not found: %v", elErr)
		}
		prop := "innerHTML"
		if in.HTMLOuter {
			prop = "outerHTML"
		}
		res, hErr := el.Eval(fmt.Sprintf(`(el) => el.%s`, prop))
		if hErr != nil {
			return fmt.Sprintf("get_html failed: %v", hErr)
		}
		html := res.Value.Str()
		if len(html) > 10000 {
			html = html[:10000] + "\n... (truncated)"
		}
		return html
	}
	html, err := page.HTML()
	if err != nil {
		return fmt.Sprintf("get_html failed: %v", err)
	}
	if len(html) > 10000 {
		html = html[:10000] + "\n... (truncated)"
	}
	return html
}

// mapKeyName converts a human-readable key name to rod's input.Key.
func mapKeyName(name string) input.Key {
	switch strings.ToLower(name) {
	case "enter", "return":
		return input.Enter
	case "tab":
		return input.Tab
	case "escape", "esc":
		return input.Escape
	case "backspace":
		return input.Backspace
	case "delete":
		return input.Delete
	case "arrowup", "up":
		return input.ArrowUp
	case "arrowdown", "down":
		return input.ArrowDown
	case "arrowleft", "left":
		return input.ArrowLeft
	case "arrowright", "right":
		return input.ArrowRight
	case "space":
		return input.Space
	case "home":
		return input.Home
	case "end":
		return input.End
	case "pageup":
		return input.PageUp
	case "pagedown":
		return input.PageDown
	case "f1":
		return input.F1
	case "f5":
		return input.F5
	case "f12":
		return input.F12
	default:
		// For single characters, use the rune directly
		if len(name) == 1 {
			return input.Key(rune(name[0]))
		}
		return input.Enter
	}
}

// truncStr truncates a string to max length.
func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
