package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Expressions are what `${{ … }}`, `if` and a loop's `until` hold:
//
//	steps.test.outcome == 'failure' && !contains(inputs.skip, 'fix')
//
// Values are what JSON has: strings, numbers (float64), booleans, null,
// lists ([]any) and objects (map[string]any). A name that is not there is
// null. false, null, "" and 0 are false; everything else is true. There
// is no clock, no environment and no I/O: the same context gives the same
// value.

// ErrExpr wraps every error an expression gives.
var ErrExpr = errors.New("expression")

// maxExprBytes bounds an expression's text.
const maxExprBytes = 4 << 10

// Expr is a parsed expression.
type Expr struct {
	src  string
	root node
}

// ParseExpr parses an expression.
func ParseExpr(src string) (*Expr, error) {
	if len(src) > maxExprBytes {
		return nil, fmt.Errorf("%w: longer than %d bytes", ErrExpr, maxExprBytes)
	}
	p := &parser{src: src}
	if err := p.lex(); err != nil {
		return nil, err
	}
	n, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, p.fail("unexpected %s", p.toks[p.pos])
	}
	return &Expr{src: src, root: n}, nil
}

// String returns the expression's text.
func (e *Expr) String() string { return e.src }

// Eval computes the expression's value in ctx.
func (e *Expr) Eval(ctx Context) (any, error) {
	v, err := e.root.eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w %q: %w", ErrExpr, e.src, err)
	}
	return v, nil
}

// Context is what an expression reads: names (inputs, trigger, steps, run,
// item, index) and the status functions' answers.
type Context struct {
	Vars map[string]any
	// Success, Failure and Cancelled answer success(), failure() and
	// cancelled() for the step being decided.
	Success, Failure, Cancelled bool
}

// Truthy reports whether v counts as true.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case int:
		return x != 0
	}
	return true
}

// Text is a value as `${{ … }}` puts it into text: strings as they are,
// numbers without needless digits, null as nothing, lists and objects as
// JSON.
func Text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// Template is text with `${{ … }}` in it.
type Template struct {
	parts []templatePart
}

type templatePart struct {
	text string
	expr *Expr
}

// ParseTemplate parses text with `${{ … }}` expressions.
func ParseTemplate(s string) (*Template, error) {
	t := &Template{}
	for {
		i := strings.Index(s, "${{")
		if i < 0 {
			if s != "" {
				t.parts = append(t.parts, templatePart{text: s})
			}
			return t, nil
		}
		if i > 0 {
			t.parts = append(t.parts, templatePart{text: s[:i]})
		}
		rest := s[i+3:]
		j := closing(rest)
		if j < 0 {
			return nil, fmt.Errorf("%w: ${{ without }}", ErrExpr)
		}
		e, err := ParseExpr(strings.TrimSpace(rest[:j]))
		if err != nil {
			return nil, err
		}
		t.parts = append(t.parts, templatePart{expr: e})
		s = rest[j+2:]
	}
}

// closing finds the }} that ends an expression, skipping quoted strings.
func closing(s string) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '}' && i+1 < len(s) && s[i+1] == '}':
			return i
		}
	}
	return -1
}

// Static reports whether the template holds no expression.
func (t *Template) Static() bool {
	return !slices.ContainsFunc(t.parts, func(p templatePart) bool { return p.expr != nil })
}

// Render replaces each expression by its value's Text.
func (t *Template) Render(ctx Context) (string, error) {
	var b strings.Builder
	for _, p := range t.parts {
		if p.expr == nil {
			b.WriteString(p.text)
			continue
		}
		v, err := p.expr.Eval(ctx)
		if err != nil {
			return "", err
		}
		b.WriteString(Text(v))
	}
	return b.String(), nil
}

// Value is the template's value: an expression alone (`${{ x }}`) keeps
// its type, anything else is text.
func (t *Template) Value(ctx Context) (any, error) {
	if len(t.parts) == 1 && t.parts[0].expr != nil {
		return t.parts[0].expr.Eval(ctx)
	}
	return t.Render(ctx)
}

// render renders s as a template; text that does not parse was refused
// when the workflow was read, so it is returned as an error here.
func render(s string, ctx Context) (string, error) {
	if !strings.Contains(s, "${{") {
		return s, nil
	}
	t, err := ParseTemplate(s)
	if err != nil {
		return "", err
	}
	return t.Render(ctx)
}

// --- Lexing ---

type tokKind int

const (
	tokName tokKind = iota
	tokString
	tokNumber
	tokOp
)

type token struct {
	kind tokKind
	text string
	num  float64
	pos  int
}

func (t token) String() string {
	if t.kind == tokString {
		return strconv.Quote(t.text)
	}
	return "'" + t.text + "'"
}

type parser struct {
	src  string
	toks []token
	pos  int
}

func (p *parser) fail(format string, args ...any) error {
	return fmt.Errorf("%w %q: %s", ErrExpr, p.src, fmt.Sprintf(format, args...))
}

var ops = []string{"==", "!=", "<=", ">=", "&&", "||", "<", ">", "!", "(", ")", "[", "]", ".", ","}

func (p *parser) lex() error {
	s := p.src
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'' || c == '"':
			var b strings.Builder
			j := i + 1
			for ; j < len(s) && s[j] != c; j++ {
				if s[j] == '\\' && j+1 < len(s) {
					j++
					switch s[j] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(s[j])
					}
					continue
				}
				b.WriteByte(s[j])
			}
			if j >= len(s) {
				return p.fail("a string is not closed")
			}
			p.toks = append(p.toks, token{kind: tokString, text: b.String(), pos: i})
			i = j + 1
		case c >= '0' && c <= '9' || c == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && p.valueExpected():
			j := i + 1
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.' || s[j] == 'e' || s[j] == 'E') {
				j++
			}
			n, err := strconv.ParseFloat(s[i:j], 64)
			if err != nil {
				return p.fail("%q is not a number", s[i:j])
			}
			p.toks = append(p.toks, token{kind: tokNumber, text: s[i:j], num: n, pos: i})
			i = j
		case c == '_' || unicode.IsLetter(rune(c)):
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] == '-' || unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j]))) {
				j++
			}
			p.toks = append(p.toks, token{kind: tokName, text: s[i:j], pos: i})
			i = j
		default:
			found := false
			for _, op := range ops {
				if strings.HasPrefix(s[i:], op) {
					p.toks = append(p.toks, token{kind: tokOp, text: op, pos: i})
					i += len(op)
					found = true
					break
				}
			}
			if !found {
				return p.fail("unexpected %q", string(c))
			}
		}
	}
	if len(p.toks) == 0 {
		return p.fail("empty")
	}
	return nil
}

// valueExpected reports whether a '-' here starts a number rather than
// following a value (there is no subtraction, but the error is clearer).
func (p *parser) valueExpected() bool {
	if len(p.toks) == 0 {
		return true
	}
	last := p.toks[len(p.toks)-1]
	return last.kind == tokOp && last.text != ")" && last.text != "]"
}

func (p *parser) peekOp(op string) bool {
	return p.pos < len(p.toks) && p.toks[p.pos].kind == tokOp && p.toks[p.pos].text == op
}

func (p *parser) expect(op string) error {
	if !p.peekOp(op) {
		if p.pos >= len(p.toks) {
			return p.fail("expected '%s' at the end", op)
		}
		return p.fail("expected '%s', not %s", op, p.toks[p.pos])
	}
	p.pos++
	return nil
}

// --- Parsing ---

func (p *parser) or() (node, error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.peekOp("||") {
		p.pos++
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		l = logical{or: true, l: l, r: r}
	}
	return l, nil
}

func (p *parser) and() (node, error) {
	l, err := p.not()
	if err != nil {
		return nil, err
	}
	for p.peekOp("&&") {
		p.pos++
		r, err := p.not()
		if err != nil {
			return nil, err
		}
		l = logical{l: l, r: r}
	}
	return l, nil
}

func (p *parser) not() (node, error) {
	if p.peekOp("!") {
		p.pos++
		n, err := p.not()
		if err != nil {
			return nil, err
		}
		return negation{n}, nil
	}
	return p.compare()
}

func (p *parser) compare() (node, error) {
	l, err := p.primary()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.peekOp(op) {
			p.pos++
			r, err := p.primary()
			if err != nil {
				return nil, err
			}
			return comparison{op: op, l: l, r: r}, nil
		}
	}
	return l, nil
}

func (p *parser) primary() (node, error) {
	if p.pos >= len(p.toks) {
		return nil, p.fail("ends early")
	}
	t := p.toks[p.pos]
	p.pos++
	var n node
	switch t.kind {
	case tokString:
		n = literal{t.text}
	case tokNumber:
		n = literal{t.num}
	case tokOp:
		if t.text != "(" {
			return nil, p.fail("unexpected %s", t)
		}
		inner, err := p.or()
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		n = inner
	case tokName:
		switch t.text {
		case "true":
			return literal{true}, nil
		case "false":
			return literal{false}, nil
		case "null":
			return literal{nil}, nil
		}
		if p.peekOp("(") {
			p.pos++
			f, ok := functions[t.text]
			if !ok {
				return nil, p.fail("no function %s (have %s)", t.text, strings.Join(functionNames(), ", "))
			}
			var args []node
			for !p.peekOp(")") {
				if len(args) > 0 {
					if err := p.expect(","); err != nil {
						return nil, err
					}
				}
				a, err := p.or()
				if err != nil {
					return nil, err
				}
				args = append(args, a)
			}
			p.pos++
			if f.min >= 0 && (len(args) < f.min || f.max >= 0 && len(args) > f.max) {
				return nil, p.fail("%s takes %s", t.text, f.arity())
			}
			n = call{name: t.text, f: f, args: args}
		} else {
			n = variable{t.text}
		}
	}
	// Members and indexes.
	for {
		switch {
		case p.peekOp("."):
			p.pos++
			if p.pos >= len(p.toks) || p.toks[p.pos].kind != tokName {
				return nil, p.fail("expected a name after '.'")
			}
			n = member{of: n, key: literal{p.toks[p.pos].text}}
			p.pos++
		case p.peekOp("["):
			p.pos++
			k, err := p.or()
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			n = member{of: n, key: k}
		default:
			return n, nil
		}
	}
}

// --- Evaluation ---

type node interface {
	eval(ctx Context) (any, error)
}

type literal struct{ v any }

func (l literal) eval(Context) (any, error) { return l.v, nil }

type variable struct{ name string }

func (v variable) eval(ctx Context) (any, error) { return normalize(ctx.Vars[v.name]), nil }

type member struct {
	of, key node
}

func (m member) eval(ctx Context) (any, error) {
	of, err := m.of.eval(ctx)
	if err != nil {
		return nil, err
	}
	k, err := m.key.eval(ctx)
	if err != nil {
		return nil, err
	}
	switch x := of.(type) {
	case map[string]any:
		return normalize(x[Text(k)]), nil
	case map[string]string:
		v, ok := x[Text(k)]
		if !ok {
			return nil, nil
		}
		return v, nil
	case []any:
		i, ok := index(k, len(x))
		if !ok {
			return nil, nil
		}
		return normalize(x[i]), nil
	case []string:
		i, ok := index(k, len(x))
		if !ok {
			return nil, nil
		}
		return x[i], nil
	}
	return nil, nil
}

// index turns k into an index of a list of n.
func index(k any, n int) (int, bool) {
	f, ok := k.(float64)
	if !ok || f != math.Trunc(f) || f < 0 || int(f) >= n {
		return 0, false
	}
	return int(f), true
}

// normalize gives Go values the shapes expressions compare: ints become
// float64, string lists become lists.
func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(x))
		for k, s := range x {
			out[k] = s
		}
		return out
	}
	return v
}

type negation struct{ n node }

func (n negation) eval(ctx Context) (any, error) {
	v, err := n.n.eval(ctx)
	if err != nil {
		return nil, err
	}
	return !Truthy(v), nil
}

// logical is && and ||: they return the deciding operand, as JavaScript
// does, so `inputs.x || 'default'` is a default.
type logical struct {
	or   bool
	l, r node
}

func (n logical) eval(ctx Context) (any, error) {
	l, err := n.l.eval(ctx)
	if err != nil {
		return nil, err
	}
	if Truthy(l) == n.or {
		return l, nil
	}
	return n.r.eval(ctx)
}

type comparison struct {
	op   string
	l, r node
}

func (c comparison) eval(ctx Context) (any, error) {
	l, err := c.l.eval(ctx)
	if err != nil {
		return nil, err
	}
	r, err := c.r.eval(ctx)
	if err != nil {
		return nil, err
	}
	switch c.op {
	case "==":
		return equal(l, r), nil
	case "!=":
		return !equal(l, r), nil
	}
	// Ordering: numbers as numbers, anything else as text.
	lf, lok := number(l)
	rf, rok := number(r)
	var cmp int
	if lok && rok {
		switch {
		case lf < rf:
			cmp = -1
		case lf > rf:
			cmp = 1
		}
	} else {
		cmp = strings.Compare(Text(l), Text(r))
	}
	switch c.op {
	case "<":
		return cmp < 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	}
	return cmp >= 0, nil
}

// equal compares like values; a number and a string holding it are equal
// (inputs are text), and strings compare as they are.
func equal(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		if y, ok := b.(string); ok {
			return x == y
		}
		if y, ok := b.(float64); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			return err == nil && f == y
		}
		return false
	case float64:
		if _, ok := b.(string); ok {
			return equal(b, a)
		}
		y, ok := b.(float64)
		return ok && x == y
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

// number reads v as a number: a number, or text holding one.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

type function struct {
	min, max int // arity; max -1 is any
	call     func(ctx Context, args []any) (any, error)
}

func (f function) arity() string {
	switch {
	case f.max == 0:
		return "no arguments"
	case f.min == f.max:
		return fmt.Sprintf("%d arguments", f.min)
	case f.max < 0:
		return fmt.Sprintf("at least %d arguments", f.min)
	}
	return fmt.Sprintf("%d to %d arguments", f.min, f.max)
}

type call struct {
	name string
	f    function
	args []node
}

func (c call) eval(ctx Context) (any, error) {
	args := make([]any, len(c.args))
	for i, a := range c.args {
		v, err := a.eval(ctx)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	v, err := c.f.call(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.name, err)
	}
	return v, nil
}

var functions = map[string]function{
	"success":   {0, 0, func(ctx Context, _ []any) (any, error) { return ctx.Success, nil }},
	"failure":   {0, 0, func(ctx Context, _ []any) (any, error) { return ctx.Failure, nil }},
	"cancelled": {0, 0, func(ctx Context, _ []any) (any, error) { return ctx.Cancelled, nil }},
	"always":    {0, 0, func(Context, []any) (any, error) { return true, nil }},
	"contains": {2, 2, func(_ Context, a []any) (any, error) {
		if list, ok := a[0].([]any); ok {
			return slices.ContainsFunc(list, func(v any) bool { return equal(v, a[1]) }), nil
		}
		return strings.Contains(strings.ToLower(Text(a[0])), strings.ToLower(Text(a[1]))), nil
	}},
	"startsWith": {2, 2, func(_ Context, a []any) (any, error) {
		return strings.HasPrefix(strings.ToLower(Text(a[0])), strings.ToLower(Text(a[1]))), nil
	}},
	"endsWith": {2, 2, func(_ Context, a []any) (any, error) {
		return strings.HasSuffix(strings.ToLower(Text(a[0])), strings.ToLower(Text(a[1]))), nil
	}},
	"join": {1, 2, func(_ Context, a []any) (any, error) {
		sep := ","
		if len(a) == 2 {
			sep = Text(a[1])
		}
		list, ok := a[0].([]any)
		if !ok {
			return Text(a[0]), nil
		}
		parts := make([]string, len(list))
		for i, v := range list {
			parts[i] = Text(v)
		}
		return strings.Join(parts, sep), nil
	}},
	"len": {1, 1, func(_ Context, a []any) (any, error) {
		switch x := a[0].(type) {
		case nil:
			return 0.0, nil
		case []any:
			return float64(len(x)), nil
		case map[string]any:
			return float64(len(x)), nil
		}
		return float64(len([]rune(Text(a[0])))), nil
	}},
	"format": {1, -1, func(_ Context, a []any) (any, error) {
		f := Text(a[0])
		var b strings.Builder
		for i := 0; i < len(f); i++ {
			switch {
			case strings.HasPrefix(f[i:], "{{"):
				b.WriteByte('{')
				i++
			case strings.HasPrefix(f[i:], "}}"):
				b.WriteByte('}')
				i++
			case f[i] == '{':
				j := strings.IndexByte(f[i:], '}')
				if j < 0 {
					return nil, errors.New("a { is not closed")
				}
				n, err := strconv.Atoi(f[i+1 : i+j])
				if err != nil || n < 0 || n+1 >= len(a) {
					return nil, fmt.Errorf("no argument for %s", f[i:i+j+1])
				}
				b.WriteString(Text(a[n+1]))
				i += j
			default:
				b.WriteByte(f[i])
			}
		}
		return b.String(), nil
	}},
	"toJSON": {1, 1, func(_ Context, a []any) (any, error) {
		data, err := json.Marshal(a[0])
		return string(data), err
	}},
	"fromJSON": {1, 1, func(_ Context, a []any) (any, error) {
		var v any
		if err := json.Unmarshal([]byte(Text(a[0])), &v); err != nil {
			return nil, fmt.Errorf("not JSON: %w", err)
		}
		return v, nil
	}},
}

func functionNames() []string {
	names := make([]string, 0, len(functions))
	for n := range functions {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// names returns the top-level names an expression reads, so a workflow
// can be checked for steps and inputs it does not have.
func (e *Expr) names() []member {
	var out []member
	var walk func(n node)
	walk = func(n node) {
		switch x := n.(type) {
		case member:
			out = append(out, x)
			walk(x.of)
			walk(x.key)
		case negation:
			walk(x.n)
		case logical:
			walk(x.l)
			walk(x.r)
		case comparison:
			walk(x.l)
			walk(x.r)
		case call:
			for _, a := range x.args {
				walk(a)
			}
		}
	}
	walk(e.root)
	return out
}

// refs returns the step IDs and inputs an expression names directly
// (steps.<id>…, steps['<id>']…, inputs.<name>), for validation.
func (e *Expr) refs() (steps, inputs []string) {
	for _, m := range e.names() {
		v, ok := m.of.(variable)
		if !ok {
			continue
		}
		k, ok := m.key.(literal)
		if !ok {
			continue
		}
		name, ok := k.v.(string)
		if !ok {
			continue
		}
		switch v.name {
		case "steps":
			steps = append(steps, name)
		case "inputs":
			inputs = append(inputs, name)
		}
	}
	return steps, inputs
}
