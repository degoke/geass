package dashboard

import (
	"fmt"
	"html/template"
	"strings"
)

// Button renders a button or link styled as a button.
func Button(label string, opts ButtonOpts) string {
	classes := []string{"btn"}
	switch opts.Variant {
	case "primary":
		classes = append(classes, "btn-primary")
	case "ghost":
		classes = append(classes, "btn-ghost")
	case "danger":
		classes = append(classes, "btn-danger")
	}
	switch opts.Size {
	case "sm":
		classes = append(classes, "btn-sm")
	case "xs":
		classes = append(classes, "btn-xs")
	}
	classAttr := strings.Join(classes, " ")
	attrs := opts.Attrs
	if attrs != "" {
		attrs = " " + attrs
	}
	escaped := template.HTMLEscapeString(label)
	if opts.Href != "" {
		return fmt.Sprintf(`<a class="%s" href="%s"%s>%s</a>`, classAttr, template.HTMLEscapeString(opts.Href), attrs, escaped)
	}
	btnType := opts.Type
	if btnType == "" {
		btnType = "button"
	}
	return fmt.Sprintf(`<button class="%s" type="%s"%s>%s</button>`, classAttr, btnType, attrs, escaped)
}

type ButtonOpts struct {
	Href    string
	Type    string
	Variant string
	Size    string
	Attrs   string
}

// Card wraps content in a surface card.
func Card(body string) string {
	return `<div class="card"><div class="card-body">` + body + `</div></div>`
}

// CardTitled renders a card with a title.
func CardTitled(title, body string) string {
	return Card(`<h3 class="card-title">` + template.HTMLEscapeString(title) + `</h3>` + body)
}

// Field wraps a labelled form control.
func Field(label, control string) string {
	return fmt.Sprintf(`<label class="field"><span class="field-label">%s</span>%s</label>`, template.HTMLEscapeString(label), control)
}

// Input renders a text input.
func Input(name, value string, attrs map[string]string) string {
	return inputElement("input", name, value, attrs)
}

// Textarea renders a multiline text input.
func Textarea(name, value string, attrs map[string]string) string {
	var b strings.Builder
	b.WriteString(`<textarea class="input`)
	if size, ok := attrs["size"]; ok && size == "sm" {
		b.WriteString(` input-sm`)
		delete(attrs, "size")
	}
	b.WriteString(`" name="`)
	b.WriteString(template.HTMLEscapeString(name))
	b.WriteString(`"`)
	for key, val := range attrs {
		if val == "" {
			fmt.Fprintf(&b, ` %s`, key)
			continue
		}
		fmt.Fprintf(&b, ` %s="%s"`, key, template.HTMLEscapeString(val))
	}
	b.WriteString(`>`)
	b.WriteString(template.HTMLEscapeString(value))
	b.WriteString(`</textarea>`)
	return b.String()
}

// Select renders a select element with options.
func Select(name string, options []SelectOption, attrs map[string]string) string {
	var b strings.Builder
	b.WriteString(`<select class="select`)
	if size, ok := attrs["size"]; ok && size == "sm" {
		b.WriteString(` select-sm`)
		delete(attrs, "size")
	}
	b.WriteString(`" name="`)
	b.WriteString(template.HTMLEscapeString(name))
	b.WriteString(`"`)
	for key, val := range attrs {
		fmt.Fprintf(&b, ` %s="%s"`, key, template.HTMLEscapeString(val))
	}
	b.WriteString(`>`)
	for _, option := range options {
		selected := ""
		if option.Selected {
			selected = ` selected`
		}
		disabled := ""
		if option.Disabled {
			disabled = ` disabled`
		}
		fmt.Fprintf(&b, `<option value="%s"%s%s>%s</option>`, template.HTMLEscapeString(option.Value), selected, disabled, template.HTMLEscapeString(option.Label))
	}
	b.WriteString(`</select>`)
	return b.String()
}

type SelectOption struct {
	Value    string
	Label    string
	Selected bool
	Disabled bool
}

func inputElement(tag, name, value string, attrs map[string]string) string {
	var b strings.Builder
	b.WriteString(`<`)
	b.WriteString(tag)
	b.WriteString(` class="input`)
	if size, ok := attrs["size"]; ok && size == "sm" {
		b.WriteString(` input-sm`)
		delete(attrs, "size")
	}
	b.WriteString(`" name="`)
	b.WriteString(template.HTMLEscapeString(name))
	b.WriteString(`"`)
	if value != "" {
		b.WriteString(` value="`)
		b.WriteString(template.HTMLEscapeString(value))
		b.WriteString(`"`)
	}
	for key, val := range attrs {
		if val == "" {
			fmt.Fprintf(&b, ` %s`, key)
			continue
		}
		fmt.Fprintf(&b, ` %s="%s"`, key, template.HTMLEscapeString(val))
	}
	b.WriteString(`>`)
	if tag != "input" {
		b.WriteString(`</`)
		b.WriteString(tag)
		b.WriteString(`>`)
	}
	return b.String()
}

// Tabs renders a horizontal tab bar.
func Tabs(items []TabItem) string {
	var b strings.Builder
	b.WriteString(`<nav class="tabs">`)
	for _, item := range items {
		active := ""
		if item.Active {
			active = " tab-active"
		}
		fmt.Fprintf(&b, `<a class="tab%s" href="%s">%s</a>`, active, template.HTMLEscapeString(item.Href), template.HTMLEscapeString(item.Label))
	}
	b.WriteString(`</nav>`)
	return b.String()
}

type TabItem struct {
	Href   string
	Label  string
	Active bool
}

// Breadcrumbs renders a breadcrumb trail.
func Breadcrumbs(items []BreadcrumbItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<nav class="breadcrumbs" aria-label="Breadcrumb">`)
	for i, item := range items {
		if i > 0 {
			b.WriteString(`<span class="breadcrumbs-sep">/</span>`)
		}
		if item.Href != "" {
			fmt.Fprintf(&b, `<a href="%s">%s</a>`, template.HTMLEscapeString(item.Href), template.HTMLEscapeString(item.Label))
		} else {
			b.WriteString(template.HTMLEscapeString(item.Label))
		}
	}
	b.WriteString(`</nav>`)
	return b.String()
}

type BreadcrumbItem struct {
	Href  string
	Label string
}

// Badge renders a status badge.
func Badge(text, variant string) string {
	class := "badge"
	switch variant {
	case "success":
		class += " badge-success"
	case "warning":
		class += " badge-warning"
	case "danger":
		class += " badge-danger"
	}
	return fmt.Sprintf(`<span class="%s">%s</span>`, class, template.HTMLEscapeString(text))
}

// Alert renders an inline alert.
func Alert(variant, message string) string {
	class := "alert"
	switch variant {
	case "warning":
		class += " alert-warning"
	case "error":
		class += " alert-error"
	}
	return fmt.Sprintf(`<div class="%s" role="alert">%s</div>`, class, template.HTMLEscapeString(message))
}

// PageHeader renders a page title with optional actions.
func PageHeader(title, actions string) string {
	if actions == "" {
		return fmt.Sprintf(`<div class="page-header"><h1 class="page-title">%s</h1></div>`, template.HTMLEscapeString(title))
	}
	return fmt.Sprintf(`<div class="page-header"><h1 class="page-title">%s</h1><div class="page-header-actions">%s</div></div>`, template.HTMLEscapeString(title), actions)
}

// Table renders a data table.
func Table(headers []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr>`)
	for _, header := range headers {
		fmt.Fprintf(&b, `<th>%s</th>`, template.HTMLEscapeString(header))
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, row := range rows {
		b.WriteString(`<tr>`)
		for _, cell := range row {
			b.WriteString(`<td>`)
			b.WriteString(cell)
			b.WriteString(`</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// FormOpen opens a form with optional HTMX attributes.
// By default, forms submit via HTMX without pushing the mutation action
// (e.g. /save) into the browser address bar. Pass your own hx-post to opt out.
// Default swap is none so handlers that redirect (HX-Redirect) update the page
// safely; fragment-returning forms must set hx-target themselves.
func FormOpen(action, method, attrs string) string {
	if method == "" {
		method = "POST"
	}
	escaped := template.HTMLEscapeString(action)
	extra := ""
	if attrs != "" {
		extra = " " + attrs
	}
	htmx := ""
	if !strings.Contains(extra, "hx-post") {
		htmx = fmt.Sprintf(` hx-post="%s" hx-target="body" hx-swap="none" hx-push-url="false"`, escaped)
	}
	return fmt.Sprintf(`<form method="%s" action="%s"%s%s>`, method, escaped, htmx, extra)
}

// CheckboxGroup renders a fieldset of checkboxes.
func CheckboxGroup(legend, name string, options []CheckboxOption) string {
	var b strings.Builder
	b.WriteString(`<fieldset class="fieldset"><legend class="fieldset-legend">`)
	b.WriteString(template.HTMLEscapeString(legend))
	b.WriteString(`</legend>`)
	for _, option := range options {
		checked := ""
		if option.Checked {
			checked = ` checked`
		}
		fmt.Fprintf(&b, `<label class="checkbox-row"><input type="checkbox" name="%s" value="%s"%s><span>%s</span></label>`, template.HTMLEscapeString(name), template.HTMLEscapeString(option.Value), checked, template.HTMLEscapeString(option.Label))
	}
	b.WriteString(`</fieldset>`)
	return b.String()
}

type CheckboxOption struct {
	Value   string
	Label   string
	Checked bool
}

// ReadinessText renders a coloured readiness message.
func ReadinessText(ok bool, yes, no string) string {
	if ok {
		return `<span class="text-success font-medium">` + template.HTMLEscapeString(yes) + `</span>`
	}
	return `<span class="text-error font-medium">` + template.HTMLEscapeString(no) + `</span>`
}
