package edgecontrol

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	MaxEdgeExprASTNodes = 64
	MaxEdgeExprDepth    = 16
	MaxEdgeExprLength   = 512
	MaxEdgeExprInputs   = 16

	edgeExprDivZeroEpsilon = 1e-12
)

var edgeExprAliasPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,31}$`)

type edgeExprValueType uint8

const (
	edgeExprTypeAny edgeExprValueType = iota
	edgeExprTypeNumber
	edgeExprTypeBoolean
	edgeExprTypeString
)

type edgeExprTokenKind uint8

const (
	edgeTokEOF edgeExprTokenKind = iota
	edgeTokNumber
	edgeTokBool
	edgeTokString
	edgeTokIdent
	edgeTokPlus
	edgeTokMinus
	edgeTokMul
	edgeTokDiv
	edgeTokEQ
	edgeTokNE
	edgeTokGT
	edgeTokGTE
	edgeTokLT
	edgeTokLTE
	edgeTokAnd
	edgeTokOr
	edgeTokNot
	edgeTokLParen
	edgeTokRParen
	edgeTokComma
)

type edgeExprToken struct {
	kind    edgeExprTokenKind
	text    string
	number  float64
	boolean bool
}

type edgeExprNodeKind uint8

const (
	edgeNodeNumber edgeExprNodeKind = iota + 1
	edgeNodeBool
	edgeNodeString
	edgeNodeIdent
	edgeNodeUnary
	edgeNodeBinary
	edgeNodeCall
)

type edgeExprNode struct {
	kind    edgeExprNodeKind
	number  float64
	boolean bool
	str     string
	op      edgeExprTokenKind
	grouped bool
	left    *edgeExprNode
	right   *edgeExprNode
	args    []*edgeExprNode
}

func isReservedEdgeExprKeyword(ident string) bool {
	switch ident {
	case "true", "false", "abs", "min", "max":
		return true
	default:
		return false
	}
}

func parseAndValidateEdgeExpression(expr string, inputs map[string]string) (*edgeExprNode, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, errors.New("expression must not be empty")
	}
	if len(expr) > MaxEdgeExprLength {
		return nil, fmt.Errorf("expression length %d exceeds maximum %d", len(expr), MaxEdgeExprLength)
	}
	if len(inputs) > MaxEdgeExprInputs {
		return nil, fmt.Errorf("expression input count %d exceeds maximum %d", len(inputs), MaxEdgeExprInputs)
	}
	for alias := range inputs {
		if !edgeExprAliasPattern.MatchString(alias) || isReservedEdgeExprKeyword(alias) {
			return nil, fmt.Errorf("expression input alias %q is invalid", alias)
		}
	}

	tokens, err := tokenizeEdgeExpression(expr)
	if err != nil {
		return nil, err
	}
	parser := &edgeExprParser{
		tokens:    tokens,
		usedIdent: make(map[string]struct{}, len(inputs)),
	}
	root, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.peek().kind != edgeTokEOF {
		return nil, fmt.Errorf("unexpected token %q in expression", parser.peek().text)
	}
	if edgeASTDepth(root) > MaxEdgeExprDepth {
		return nil, fmt.Errorf("expression nesting depth exceeds maximum %d", MaxEdgeExprDepth)
	}

	var missing []string
	for ident := range parser.usedIdent {
		if _, ok := inputs[ident]; !ok {
			missing = append(missing, ident)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("expression references undeclared input(s): %s", strings.Join(missing, ", "))
	}

	var unused []string
	for alias := range inputs {
		if _, ok := parser.usedIdent[alias]; !ok {
			unused = append(unused, alias)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		return nil, fmt.Errorf("expression declares unused input(s): %s", strings.Join(unused, ", "))
	}

	rootType, err := checkEdgeExprNodeType(root)
	if err != nil {
		return nil, err
	}
	if rootType != edgeExprTypeBoolean {
		return nil, errors.New("expression root must evaluate to a boolean")
	}
	return root, nil
}

func tokenizeEdgeExpression(expr string) ([]edgeExprToken, error) {
	tokens := make([]edgeExprToken, 0, 16)
	for i := 0; i < len(expr); {
		ch := expr[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			str, nextIdx, err := scanEdgeExprString(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, edgeExprToken{kind: edgeTokString, text: str})
			i = nextIdx
			continue
		}
		if (ch >= '0' && ch <= '9') || (ch == '.' && i+1 < len(expr) && expr[i+1] >= '0' && expr[i+1] <= '9') {
			val, raw, nextIdx, err := scanEdgeExprNumber(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, edgeExprToken{kind: edgeTokNumber, text: raw, number: val})
			i = nextIdx
			continue
		}
		if isEdgeIdentStart(ch) {
			end := i + 1
			for end < len(expr) && isEdgeIdentContinue(expr[end]) {
				end++
			}
			raw := expr[i:end]
			if len(raw) > 32 {
				return nil, fmt.Errorf("expression identifier %q exceeds 32 characters", raw)
			}
			switch raw {
			case "true":
				tokens = append(tokens, edgeExprToken{kind: edgeTokBool, text: raw, boolean: true})
			case "false":
				tokens = append(tokens, edgeExprToken{kind: edgeTokBool, text: raw, boolean: false})
			default:
				tokens = append(tokens, edgeExprToken{kind: edgeTokIdent, text: raw})
			}
			i = end
			continue
		}

		two := ""
		if i+1 < len(expr) {
			two = expr[i : i+2]
		}
		switch two {
		case "==":
			tokens = append(tokens, edgeExprToken{kind: edgeTokEQ, text: "=="})
			i += 2
			continue
		case "!=":
			tokens = append(tokens, edgeExprToken{kind: edgeTokNE, text: "!="})
			i += 2
			continue
		case ">=":
			tokens = append(tokens, edgeExprToken{kind: edgeTokGTE, text: ">="})
			i += 2
			continue
		case "<=":
			tokens = append(tokens, edgeExprToken{kind: edgeTokLTE, text: "<="})
			i += 2
			continue
		case "&&":
			tokens = append(tokens, edgeExprToken{kind: edgeTokAnd, text: "&&"})
			i += 2
			continue
		case "||":
			tokens = append(tokens, edgeExprToken{kind: edgeTokOr, text: "||"})
			i += 2
			continue
		}

		switch ch {
		case '+':
			tokens = append(tokens, edgeExprToken{kind: edgeTokPlus, text: "+"})
		case '-':
			tokens = append(tokens, edgeExprToken{kind: edgeTokMinus, text: "-"})
		case '*':
			tokens = append(tokens, edgeExprToken{kind: edgeTokMul, text: "*"})
		case '/':
			tokens = append(tokens, edgeExprToken{kind: edgeTokDiv, text: "/"})
		case '>':
			tokens = append(tokens, edgeExprToken{kind: edgeTokGT, text: ">"})
		case '<':
			tokens = append(tokens, edgeExprToken{kind: edgeTokLT, text: "<"})
		case '!':
			tokens = append(tokens, edgeExprToken{kind: edgeTokNot, text: "!"})
		case '(':
			tokens = append(tokens, edgeExprToken{kind: edgeTokLParen, text: "("})
		case ')':
			tokens = append(tokens, edgeExprToken{kind: edgeTokRParen, text: ")"})
		case ',':
			tokens = append(tokens, edgeExprToken{kind: edgeTokComma, text: ","})
		default:
			return nil, fmt.Errorf("unexpected character %q in expression", ch)
		}
		i++
	}
	tokens = append(tokens, edgeExprToken{kind: edgeTokEOF})
	return tokens, nil
}

func scanEdgeExprString(expr string, start int) (string, int, error) {
	quote := expr[start]
	var builder strings.Builder
	for i := start + 1; i < len(expr); i++ {
		ch := expr[i]
		if ch == quote {
			return builder.String(), i + 1, nil
		}
		if ch == '\n' || ch == '\r' {
			return "", 0, errors.New("unterminated string literal in expression")
		}
		if ch == '\\' {
			i++
			if i >= len(expr) {
				return "", 0, errors.New("unterminated escape sequence in expression")
			}
			switch expr[i] {
			case '\\', '\'', '"':
				builder.WriteByte(expr[i])
			case 'n':
				builder.WriteByte('\n')
			case 't':
				builder.WriteByte('\t')
			case 'r':
				builder.WriteByte('\r')
			default:
				return "", 0, fmt.Errorf("unsupported escape sequence \\%c in expression", expr[i])
			}
			continue
		}
		builder.WriteByte(ch)
	}
	return "", 0, errors.New("unterminated string literal in expression")
}

func scanEdgeExprNumber(expr string, start int) (float64, string, int, error) {
	i := start
	hasDigits := false
	for i < len(expr) && expr[i] >= '0' && expr[i] <= '9' {
		hasDigits = true
		i++
	}
	if i < len(expr) && expr[i] == '.' {
		i++
		fracDigits := false
		for i < len(expr) && expr[i] >= '0' && expr[i] <= '9' {
			fracDigits = true
			hasDigits = true
			i++
		}
		if !fracDigits {
			return 0, "", 0, fmt.Errorf("invalid numeric literal near %q", expr[start:i])
		}
	}
	if !hasDigits {
		return 0, "", 0, fmt.Errorf("invalid numeric literal near %q", expr[start:i])
	}
	if i < len(expr) && (expr[i] == 'e' || expr[i] == 'E') {
		i++
		if i < len(expr) && (expr[i] == '+' || expr[i] == '-') {
			i++
		}
		expDigits := false
		for i < len(expr) && expr[i] >= '0' && expr[i] <= '9' {
			expDigits = true
			i++
		}
		if !expDigits {
			return 0, "", 0, fmt.Errorf("invalid exponent in numeric literal near %q", expr[start:i])
		}
	}
	if i < len(expr) && (isEdgeIdentStart(expr[i]) || expr[i] == '.') {
		return 0, "", 0, fmt.Errorf("invalid numeric literal near %q", expr[start:i+1])
	}
	raw := expr[start:i]
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil || !isFiniteFloat(val) {
		return 0, "", 0, fmt.Errorf("invalid numeric literal %q", raw)
	}
	return val, raw, i, nil
}

func isEdgeIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isEdgeIdentContinue(ch byte) bool {
	return isEdgeIdentStart(ch) || (ch >= '0' && ch <= '9')
}

type edgeExprParser struct {
	tokens     []edgeExprToken
	pos        int
	nodes      int
	parenDepth int
	usedIdent  map[string]struct{}
}

func (p *edgeExprParser) peek() edgeExprToken {
	if p.pos >= len(p.tokens) {
		return edgeExprToken{kind: edgeTokEOF}
	}
	return p.tokens[p.pos]
}

func (p *edgeExprParser) advance() edgeExprToken {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *edgeExprParser) newNode(node edgeExprNode) (*edgeExprNode, error) {
	p.nodes++
	if p.nodes > MaxEdgeExprASTNodes {
		return nil, fmt.Errorf("expression exceeds maximum AST node count (%d)", MaxEdgeExprASTNodes)
	}
	return &node, nil
}

func (p *edgeExprParser) parseOr() (*edgeExprNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == edgeTokOr {
		op := p.advance().kind
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(edgeExprNode{kind: edgeNodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *edgeExprParser) parseAnd() (*edgeExprNode, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == edgeTokAnd {
		op := p.advance().kind
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(edgeExprNode{kind: edgeNodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *edgeExprParser) parseEquality() (*edgeExprNode, error) {
	left, err := p.parseRelational()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == edgeTokEQ || p.peek().kind == edgeTokNE {
		op := p.advance().kind
		right, err := p.parseRelational()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(edgeExprNode{kind: edgeNodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *edgeExprParser) parseRelational() (*edgeExprNode, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	for {
		kind := p.peek().kind
		if kind != edgeTokGT && kind != edgeTokGTE && kind != edgeTokLT && kind != edgeTokLTE {
			break
		}
		p.advance()
		right, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(edgeExprNode{kind: edgeNodeBinary, op: kind, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *edgeExprParser) parseAdditive() (*edgeExprNode, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == edgeTokPlus || p.peek().kind == edgeTokMinus {
		op := p.advance().kind
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(edgeExprNode{kind: edgeNodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *edgeExprParser) parseMultiplicative() (*edgeExprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == edgeTokMul || p.peek().kind == edgeTokDiv {
		op := p.advance().kind
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(edgeExprNode{kind: edgeNodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *edgeExprParser) parseUnary() (*edgeExprNode, error) {
	if p.peek().kind == edgeTokNot || p.peek().kind == edgeTokMinus {
		op := p.advance().kind
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return p.newNode(edgeExprNode{kind: edgeNodeUnary, op: op, left: operand})
	}
	return p.parsePrimary()
}

func (p *edgeExprParser) parsePrimary() (*edgeExprNode, error) {
	tok := p.peek()
	switch tok.kind {
	case edgeTokNumber:
		p.advance()
		return p.newNode(edgeExprNode{kind: edgeNodeNumber, number: tok.number})
	case edgeTokBool:
		p.advance()
		return p.newNode(edgeExprNode{kind: edgeNodeBool, boolean: tok.boolean})
	case edgeTokString:
		p.advance()
		return p.newNode(edgeExprNode{kind: edgeNodeString, str: tok.text})
	case edgeTokIdent:
		p.advance()
		if tok.text == "abs" || tok.text == "min" || tok.text == "max" {
			if p.peek().kind != edgeTokLParen {
				return nil, fmt.Errorf("reserved keyword %q cannot be used as identifier", tok.text)
			}
			return p.parseCall(tok.text)
		}
		if p.peek().kind == edgeTokLParen {
			return nil, fmt.Errorf("unknown expression function %q", tok.text)
		}
		p.usedIdent[tok.text] = struct{}{}
		return p.newNode(edgeExprNode{kind: edgeNodeIdent, str: tok.text})
	case edgeTokLParen:
		p.advance()
		p.parenDepth++
		if p.parenDepth > MaxEdgeExprDepth {
			return nil, fmt.Errorf("expression nesting depth exceeds maximum %d", MaxEdgeExprDepth)
		}
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != edgeTokRParen {
			return nil, errors.New("expected ')' in expression")
		}
		p.advance()
		p.parenDepth--
		inner.grouped = true
		return inner, nil
	default:
		if tok.kind == edgeTokEOF {
			return nil, errors.New("unexpected end of expression")
		}
		return nil, fmt.Errorf("unexpected token %q in expression", tok.text)
	}
}

func (p *edgeExprParser) parseCall(name string) (*edgeExprNode, error) {
	p.advance()
	p.parenDepth++
	if p.parenDepth > MaxEdgeExprDepth {
		return nil, fmt.Errorf("expression nesting depth exceeds maximum %d", MaxEdgeExprDepth)
	}
	if p.peek().kind == edgeTokRParen {
		return nil, fmt.Errorf("function %s requires arguments", name)
	}
	args := make([]*edgeExprNode, 0, 2)
	for {
		arg, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.peek().kind != edgeTokComma {
			break
		}
		p.advance()
	}
	if p.peek().kind != edgeTokRParen {
		return nil, fmt.Errorf("expected ')' after arguments to %s", name)
	}
	p.advance()
	p.parenDepth--
	switch name {
	case "abs":
		if len(args) != 1 {
			return nil, fmt.Errorf("abs requires 1 argument, got %d", len(args))
		}
	case "min", "max":
		if len(args) < 2 || len(args) > 8 {
			return nil, fmt.Errorf("%s requires 2 to 8 arguments, got %d", name, len(args))
		}
	}
	return p.newNode(edgeExprNode{kind: edgeNodeCall, str: name, args: args})
}

func edgeASTDepth(node *edgeExprNode) int {
	if node == nil {
		return 0
	}
	if node.kind == edgeNodeBinary {
		leftDepth := edgeASTDepth(node.left)
		if node.left != nil && !node.left.grouped && node.left.kind == edgeNodeBinary && node.left.op == node.op {
			leftDepth--
		}
		rightDepth := edgeASTDepth(node.right)
		best := leftDepth
		if rightDepth > best {
			best = rightDepth
		}
		return 1 + best
	}
	best := 0
	if d := edgeASTDepth(node.left); d > best {
		best = d
	}
	if d := edgeASTDepth(node.right); d > best {
		best = d
	}
	for _, arg := range node.args {
		if d := edgeASTDepth(arg); d > best {
			best = d
		}
	}
	return 1 + best
}

func checkEdgeExprNodeType(node *edgeExprNode) (edgeExprValueType, error) {
	switch node.kind {
	case edgeNodeNumber:
		return edgeExprTypeNumber, nil
	case edgeNodeBool:
		return edgeExprTypeBoolean, nil
	case edgeNodeString:
		return edgeExprTypeString, nil
	case edgeNodeIdent:
		return edgeExprTypeAny, nil
	case edgeNodeUnary:
		operandType, err := checkEdgeExprNodeType(node.left)
		if err != nil {
			return edgeExprTypeAny, err
		}
		if node.op == edgeTokNot {
			if operandType != edgeExprTypeBoolean && operandType != edgeExprTypeAny {
				return edgeExprTypeAny, errors.New("logical '!' requires a boolean operand")
			}
			return edgeExprTypeBoolean, nil
		}
		if operandType != edgeExprTypeNumber && operandType != edgeExprTypeAny {
			return edgeExprTypeAny, errors.New("unary '-' requires a numeric operand")
		}
		return edgeExprTypeNumber, nil
	case edgeNodeCall:
		for _, arg := range node.args {
			argType, err := checkEdgeExprNodeType(arg)
			if err != nil {
				return edgeExprTypeAny, err
			}
			if argType != edgeExprTypeNumber && argType != edgeExprTypeAny {
				return edgeExprTypeAny, fmt.Errorf("function %s requires numeric arguments", node.str)
			}
		}
		return edgeExprTypeNumber, nil
	case edgeNodeBinary:
		leftType, err := checkEdgeExprNodeType(node.left)
		if err != nil {
			return edgeExprTypeAny, err
		}
		rightType, err := checkEdgeExprNodeType(node.right)
		if err != nil {
			return edgeExprTypeAny, err
		}
		switch node.op {
		case edgeTokPlus, edgeTokMinus, edgeTokMul, edgeTokDiv:
			if (leftType != edgeExprTypeNumber && leftType != edgeExprTypeAny) || (rightType != edgeExprTypeNumber && rightType != edgeExprTypeAny) {
				return edgeExprTypeAny, errors.New("arithmetic operator requires numeric operands")
			}
			return edgeExprTypeNumber, nil
		case edgeTokGT, edgeTokGTE, edgeTokLT, edgeTokLTE:
			if (leftType != edgeExprTypeNumber && leftType != edgeExprTypeAny) || (rightType != edgeExprTypeNumber && rightType != edgeExprTypeAny) {
				return edgeExprTypeAny, errors.New("ordered comparison operator requires numeric operands")
			}
			return edgeExprTypeBoolean, nil
		case edgeTokAnd, edgeTokOr:
			if (leftType != edgeExprTypeBoolean && leftType != edgeExprTypeAny) || (rightType != edgeExprTypeBoolean && rightType != edgeExprTypeAny) {
				return edgeExprTypeAny, errors.New("logical operator requires boolean operands")
			}
			return edgeExprTypeBoolean, nil
		case edgeTokEQ, edgeTokNE:
			if leftType != edgeExprTypeAny && rightType != edgeExprTypeAny && leftType != rightType {
				return edgeExprTypeAny, errors.New("equality operator compares incompatible types")
			}
			return edgeExprTypeBoolean, nil
		}
	}
	return edgeExprTypeAny, errors.New("unsupported expression AST node")
}
