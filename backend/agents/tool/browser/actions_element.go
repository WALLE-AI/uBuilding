package browser

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

func (t *BrowserTool) doFindElement(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v\nLocator: %s", err, in.Locator)
	}
	return describeElement(el)
}

func (t *BrowserTool) doFindElements(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	els, err := t.resolveElements(page, in)
	if err != nil {
		return fmt.Sprintf("Elements not found: %v", err)
	}
	max := in.MaxResults
	if max <= 0 {
		max = 20
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Found %d element(s) (showing up to %d):", len(els), max))
	for i, el := range els {
		if i >= max {
			lines = append(lines, fmt.Sprintf("  ... and %d more", len(els)-max))
			break
		}
		lines = append(lines, fmt.Sprintf("  [%d] %s", i, briefElement(el)))
	}
	return strings.Join(lines, "\n")
}

func (t *BrowserTool) doGetElementInfo(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}
	return describeElement(el)
}

func (t *BrowserTool) doGetElementState(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}
	return elementStates(el)
}

func (t *BrowserTool) doFindChild(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.ParentLocator == "" {
		return "Error: parent_locator is required for find_child"
	}

	parentResolved := Resolve(in.ParentLocator)
	var parent *rod.Element
	switch parentResolved.Strategy {
	case StrategyXPath:
		parent, err = page.ElementX(parentResolved.Value)
	default:
		parent, err = page.Element(parentResolved.Value)
	}
	if err != nil {
		return fmt.Sprintf("Parent not found: %v", err)
	}

	childResolved := Resolve(in.Locator)
	var child *rod.Element
	switch childResolved.Strategy {
	case StrategyXPath:
		child, err = parent.ElementX(childResolved.Value)
	default:
		child, err = parent.Element(childResolved.Value)
	}
	if err != nil {
		return fmt.Sprintf("Child not found under parent: %v", err)
	}
	return describeElement(child)
}

// describeElement returns detailed info about an element.
func describeElement(el *rod.Element) string {
	tag := ""
	text := ""
	attrs := map[string]string{}

	res, err := el.Eval(`(el) => {
		let a = {};
		for (let attr of el.attributes || []) { a[attr.name] = attr.value; }
		return {
			tag: el.tagName.toLowerCase(),
			text: (el.innerText || '').substring(0, 200),
			attrs: a,
			visible: el.offsetParent !== null || el.tagName === 'BODY',
			rect: el.getBoundingClientRect().toJSON()
		};
	}`)
	if err == nil {
		v := res.Value
		tag = v.Get("tag").Str()
		text = v.Get("text").Str()
		if raw, ok := v.Get("attrs").Raw().(map[string]interface{}); ok {
			for ak, av := range raw {
				attrs[ak] = fmt.Sprintf("%v", av)
			}
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Element found: <%s>", tag))
	if text != "" {
		if len(text) > 100 {
			text = text[:100] + "..."
		}
		lines = append(lines, fmt.Sprintf("  text: %q", text))
	}
	for k, v := range attrs {
		if len(v) > 80 {
			v = v[:80] + "..."
		}
		lines = append(lines, fmt.Sprintf("  @%s=%q", k, v))
	}
	return strings.Join(lines, "\n")
}

// briefElement returns a one-line summary of an element.
func briefElement(el *rod.Element) string {
	res, err := el.Eval(`(el) => {
		let id = el.id ? '#'+el.id : '';
		let cls = el.className ? '.'+el.className.split(' ')[0] : '';
		let txt = (el.innerText || '').substring(0, 40);
		return el.tagName.toLowerCase() + id + cls + (txt ? ' "'+txt+'"' : '');
	}`)
	if err != nil {
		return "(unknown)"
	}
	return res.Value.Str()
}

// elementStates returns six-dimensional state of an element.
func elementStates(el *rod.Element) string {
	res, err := el.Eval(`(el) => {
		let rect = el.getBoundingClientRect();
		let visible = rect.width > 0 && rect.height > 0 && el.offsetParent !== null;
		let enabled = !el.disabled;
		let inViewport = rect.top >= 0 && rect.left >= 0 &&
			rect.bottom <= window.innerHeight && rect.right <= window.innerWidth;
		let covered = false;
		if (visible) {
			let cx = rect.left + rect.width/2;
			let cy = rect.top + rect.height/2;
			let topEl = document.elementFromPoint(cx, cy);
			covered = topEl !== el && !el.contains(topEl);
		}
		return {
			visible: visible,
			enabled: enabled,
			in_viewport: inViewport,
			covered: covered,
			clickable: visible && enabled && !covered,
			tag: el.tagName.toLowerCase(),
			rect: { x: rect.x, y: rect.y, w: rect.width, h: rect.height }
		};
	}`)
	if err != nil {
		return fmt.Sprintf("Error getting element state: %v", err)
	}

	v := res.Value
	var lines []string
	lines = append(lines, "Element state:")
	lines = append(lines, fmt.Sprintf("  visible:     %v", v.Get("visible").Bool()))
	lines = append(lines, fmt.Sprintf("  enabled:     %v", v.Get("enabled").Bool()))
	lines = append(lines, fmt.Sprintf("  in_viewport: %v", v.Get("in_viewport").Bool()))
	lines = append(lines, fmt.Sprintf("  covered:     %v", v.Get("covered").Bool()))
	lines = append(lines, fmt.Sprintf("  clickable:   %v", v.Get("clickable").Bool()))
	rect := v.Get("rect")
	lines = append(lines, fmt.Sprintf("  rect: (%.0f, %.0f, %.0f×%.0f)",
		rect.Get("x").Num(), rect.Get("y").Num(), rect.Get("w").Num(), rect.Get("h").Num()))
	return strings.Join(lines, "\n")
}

// waitForElementState waits for an element to reach a specific state.
func (t *BrowserTool) waitForElementState(page *rod.Page, locator string, state string, timeout time.Duration) (*rod.Element, error) {
	resolved := Resolve(locator)
	page = page.Timeout(timeout)

	switch state {
	case "visible":
		var el *rod.Element
		var err error
		if resolved.Strategy == StrategyXPath {
			el, err = page.ElementX(resolved.Value)
		} else {
			el, err = page.Element(resolved.Value)
		}
		if err != nil {
			return nil, err
		}
		err = el.WaitVisible()
		return el, err
	case "hidden":
		var el *rod.Element
		var err error
		if resolved.Strategy == StrategyXPath {
			el, err = page.ElementX(resolved.Value)
		} else {
			el, err = page.Element(resolved.Value)
		}
		if err != nil {
			return nil, err
		}
		err = el.WaitInvisible()
		return el, err
	case "present", "attached":
		if resolved.Strategy == StrategyXPath {
			return page.ElementX(resolved.Value)
		}
		return page.Element(resolved.Value)
	case "absent", "detached":
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			var found bool
			if resolved.Strategy == StrategyXPath {
				els, _ := page.ElementsX(resolved.Value)
				found = len(els) > 0
			} else {
				els, _ := page.Elements(resolved.Value)
				found = len(els) > 0
			}
			if !found {
				return nil, nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return nil, fmt.Errorf("element still present after timeout")
	case "enabled":
		var el *rod.Element
		var err error
		if resolved.Strategy == StrategyXPath {
			el, err = page.ElementX(resolved.Value)
		} else {
			el, err = page.Element(resolved.Value)
		}
		if err != nil {
			return nil, err
		}
		err = el.WaitEnabled()
		return el, err
	case "clickable":
		var el *rod.Element
		var err error
		if resolved.Strategy == StrategyXPath {
			el, err = page.ElementX(resolved.Value)
		} else {
			el, err = page.Element(resolved.Value)
		}
		if err != nil {
			return nil, err
		}
		err = el.WaitVisible()
		if err != nil {
			return nil, err
		}
		err = el.WaitEnabled()
		return el, err
	default:
		return nil, fmt.Errorf("unknown wait_state: %q (use visible/hidden/present/absent/enabled/clickable)", state)
	}
}
