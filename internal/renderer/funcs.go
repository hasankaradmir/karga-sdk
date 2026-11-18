package renderer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
)

// tickFunc is the function instrument puts at the start of every range body. It is not meant to be
// called by templates; calling it only spends budget.
const tickFunc = "kargaLoopTick"

// tick charges one loop iteration to the budget. text/template cannot be interrupted while it runs
// and a loop that writes nothing never reaches the output limit, so every iteration of every range
// counts and checks the deadline.
func (b *budget) tick() (string, error) {
	b.iterations++
	if b.iterations > maxIterations {
		return "", fmt.Errorf("rendering exceeds %d loop iterations", maxIterations)
	}
	if b.iterations%1024 != 0 { // reading the clock costs more than the iteration
		return "", nil
	}
	return "", b.check()
}

// instrument puts a call of tickFunc at the start of every range body of t and the templates
// associated with it. Trees already instrumented are left as they are, so it may run again after a
// tpl parses more templates.
func instrument(t *template.Template) {
	for _, tt := range t.Templates() {
		if tt.Tree != nil && tt.Root != nil {
			instrumentList(tt.Root)
		}
	}
}

func instrumentList(l *parse.ListNode) {
	if l == nil {
		return
	}
	for _, n := range l.Nodes {
		switch n := n.(type) {
		case *parse.RangeNode:
			instrumentList(n.List)
			instrumentList(n.ElseList)
			if n.List != nil && !startsWithTick(n.List) {
				n.List.Nodes = append([]parse.Node{tickNode(n.Position())}, n.List.Nodes...)
			}
		case *parse.IfNode:
			instrumentList(n.List)
			instrumentList(n.ElseList)
		case *parse.WithNode:
			instrumentList(n.List)
			instrumentList(n.ElseList)
		case *parse.ListNode:
			instrumentList(n)
		}
	}
}

func tickNode(pos parse.Pos) *parse.ActionNode {
	return &parse.ActionNode{NodeType: parse.NodeAction, Pos: pos, Pipe: &parse.PipeNode{NodeType: parse.NodePipe, Pos: pos,
		Cmds: []*parse.CommandNode{{NodeType: parse.NodeCommand, Pos: pos,
			Args: []parse.Node{&parse.IdentifierNode{NodeType: parse.NodeIdentifier, Pos: pos, Ident: tickFunc}}}}}}
}

func startsWithTick(l *parse.ListNode) bool {
	if len(l.Nodes) == 0 {
		return false
	}
	a, ok := l.Nodes[0].(*parse.ActionNode)
	if !ok || a.Pipe == nil || len(a.Pipe.Cmds) != 1 || len(a.Pipe.Cmds[0].Args) != 1 {
		return false
	}
	id, ok := a.Pipe.Cmds[0].Args[0].(*parse.IdentifierNode)
	return ok && id.Ident == tickFunc
}

// The sprig functions whose result size an argument controls are replaced by these, which check the
// size before they build anything. Their results are sprig's for every argument they accept.

func tooLarge(name string) error {
	return fmt.Errorf("%s: result too large (limit %d)", name, maxItems)
}

// steps is how many values start, start+step, … lie before stop (exclusive) or up to it
// (inclusive), computed without overflow; 0 when step points away from stop or is 0.
func steps(start, stop, step int, inclusive bool) uint64 {
	var span, by uint64
	switch {
	case step > 0 && stop > start:
		span, by = uint64(stop)-uint64(start), uint64(step)
	case step < 0 && stop < start:
		span, by = uint64(start)-uint64(stop), uint64(-(step+1))+1
	case inclusive && stop == start && step != 0:
		return 1
	default:
		return 0
	}
	n := span / by
	if inclusive || span%by != 0 {
		n++
	}
	return n
}

// progression is start, start+step, … for n values; n is at most maxItems, so it never overflows
// past the values steps counted.
func progression(start, step int, n uint64) []int {
	v := make([]int, n)
	for i := range v {
		v[i] = start + i*step
	}
	return v
}

func untilStep(start, stop, step int) ([]int, error) {
	n := steps(start, stop, step, false)
	if n > maxItems {
		return nil, tooLarge("untilStep")
	}
	return progression(start, step, n), nil
}

func until(count int) ([]int, error) {
	step := 1
	if count < 0 {
		step = -1
	}
	v, err := untilStep(0, count, step)
	if err != nil {
		return nil, tooLarge("until")
	}
	return v, nil
}

// seq prints the numbers of seq END, seq START END or seq START STEP END, inclusive of END.
func seq(params ...int) (string, error) {
	var start, step, end int
	switch len(params) {
	case 1:
		start, step, end = 1, 1, params[0]
		if end < start {
			step = -1
		}
	case 2:
		start, step, end = params[0], 1, params[1]
		if end < start {
			step = -1
		}
	case 3:
		start, step, end = params[0], params[1], params[2]
	default:
		return "", nil
	}
	n := steps(start, end, step, true)
	if n > maxItems {
		return "", tooLarge("seq")
	}
	var sb strings.Builder
	for i, v := range progression(start, step, n) {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(strconv.Itoa(v))
	}
	return sb.String(), nil
}

func repeat(count int, s string) (string, error) {
	if count < 0 {
		return "", errors.New("repeat: negative count")
	}
	if count > maxOutput/max(len(s), 1) {
		return "", fmt.Errorf("repeat: result too large (limit %d bytes)", maxOutput)
	}
	return strings.Repeat(s, count), nil
}

func indent(spaces int, s string) (string, error) {
	if spaces < 0 {
		return "", errors.New("indent: negative width")
	}
	if lines := strings.Count(s, "\n") + 1; spaces > maxOutput/lines {
		return "", fmt.Errorf("indent: result too large (limit %d bytes)", maxOutput)
	}
	pad := strings.Repeat(" ", spaces)
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad), nil
}

func nindent(spaces int, s string) (string, error) {
	out, err := indent(spaces, s)
	if err != nil {
		return "", fmt.Errorf("n%w", err)
	}
	return "\n" + out, nil
}

// sizeFuncs are the replacements, by sprig name.
var sizeFuncs = template.FuncMap{
	"until": until, "untilStep": untilStep, "seq": seq,
	"repeat": repeat, "indent": indent, "nindent": nindent,
}
