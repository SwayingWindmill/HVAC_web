package alarmservice

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MaxExpressionASTNodes = 64
	MaxExpressionDepth    = 16
	MaxExpressionLength   = 512
	MaxExpressionInputs   = 16

	exprDivZeroEpsilon = 1e-12
)

var exprAliasPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,31}$`)

type exprValueType uint8

const (
	exprTypeAny exprValueType = iota
	exprTypeNumber
	exprTypeBoolean
	exprTypeString
)

type exprTokenKind uint8

const (
	tokEOF exprTokenKind = iota
	tokNumber
	tokBool
	tokString
	tokIdent
	tokPlus
	tokMinus
	tokMul
	tokDiv
	tokEQ
	tokNE
	tokGT
	tokGTE
	tokLT
	tokLTE
	tokAnd
	tokOr
	tokNot
	tokLParen
	tokRParen
	tokComma
)

type exprToken struct {
	kind    exprTokenKind
	text    string
	number  float64
	boolean bool
}

type exprNodeKind uint8

const (
	nodeNumber exprNodeKind = iota + 1
	nodeBool
	nodeString
	nodeIdent
	nodeUnary
	nodeBinary
	nodeCall
)

type exprNode struct {
	kind    exprNodeKind
	number  float64
	boolean bool
	str     string
	op      exprTokenKind
	grouped bool
	left    *exprNode
	right   *exprNode
	args    []*exprNode
}

// ValidateExpression parses expr, verifies syntax and resource bounds (length, AST node count,
// nesting depth, and input count), checks that identifiers and declared inputs match 1:1, and
// statically proves operator types and a boolean root result.
func ValidateExpression(expr string, inputs map[string]string) error {
	_, err := parseAndValidateExpression(expr, inputs)
	return err
}

func isReservedExprKeyword(ident string) bool {
	switch ident {
	case "true", "false", "abs", "min", "max":
		return true
	default:
		return false
	}
}

func parseAndValidateExpression(expr string, inputs map[string]string) (*exprNode, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, errors.New("expression must not be empty")
	}
	if len(expr) > MaxExpressionLength {
		return nil, fmt.Errorf("expression length %d exceeds maximum %d", len(expr), MaxExpressionLength)
	}
	if len(inputs) > MaxExpressionInputs {
		return nil, fmt.Errorf("expression input count %d exceeds maximum %d", len(inputs), MaxExpressionInputs)
	}
	for alias := range inputs {
		if !exprAliasPattern.MatchString(alias) || isReservedExprKeyword(alias) {
			return nil, fmt.Errorf("expression input alias %q is invalid", alias)
		}
	}

	tokens, err := tokenizeExpression(expr)
	if err != nil {
		return nil, err
	}
	parser := &exprParser{
		tokens:    tokens,
		usedIdent: make(map[string]struct{}, len(inputs)),
	}
	root, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected token %q in expression", parser.peek().text)
	}
	if astDepth(root) > MaxExpressionDepth {
		return nil, fmt.Errorf("expression nesting depth exceeds maximum %d", MaxExpressionDepth)
	}

	missing := make([]string, 0)
	for ident := range parser.usedIdent {
		if _, ok := inputs[ident]; !ok {
			missing = append(missing, ident)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("expression references undeclared input(s): %s", strings.Join(missing, ", "))
	}

	unused := make([]string, 0)
	for alias := range inputs {
		if _, ok := parser.usedIdent[alias]; !ok {
			unused = append(unused, alias)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		return nil, fmt.Errorf("expression declares unused input(s): %s", strings.Join(unused, ", "))
	}

	rootType, err := checkExprNodeType(root)
	if err != nil {
		return nil, err
	}
	if rootType != exprTypeBoolean {
		return nil, errors.New("expression root must evaluate to a boolean")
	}
	return root, nil
}

func tokenizeExpression(expr string) ([]exprToken, error) {
	tokens := make([]exprToken, 0, 16)
	for i := 0; i < len(expr); {
		ch := expr[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			str, nextIdx, err := scanStringLiteral(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, exprToken{kind: tokString, text: str})
			i = nextIdx
			continue
		}
		if (ch >= '0' && ch <= '9') || (ch == '.' && i+1 < len(expr) && expr[i+1] >= '0' && expr[i+1] <= '9') {
			val, raw, nextIdx, err := scanNumberLiteral(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, exprToken{kind: tokNumber, text: raw, number: val})
			i = nextIdx
			continue
		}
		if isIdentStart(ch) {
			end := i + 1
			for end < len(expr) && isIdentContinue(expr[end]) {
				end++
			}
			raw := expr[i:end]
			if len(raw) > 32 {
				return nil, fmt.Errorf("expression identifier %q exceeds 32 characters", raw)
			}
			switch raw {
			case "true":
				tokens = append(tokens, exprToken{kind: tokBool, text: raw, boolean: true})
			case "false":
				tokens = append(tokens, exprToken{kind: tokBool, text: raw, boolean: false})
			default:
				tokens = append(tokens, exprToken{kind: tokIdent, text: raw})
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
			tokens = append(tokens, exprToken{kind: tokEQ, text: "=="})
			i += 2
			continue
		case "!=":
			tokens = append(tokens, exprToken{kind: tokNE, text: "!="})
			i += 2
			continue
		case ">=":
			tokens = append(tokens, exprToken{kind: tokGTE, text: ">="})
			i += 2
			continue
		case "<=":
			tokens = append(tokens, exprToken{kind: tokLTE, text: "<="})
			i += 2
			continue
		case "&&":
			tokens = append(tokens, exprToken{kind: tokAnd, text: "&&"})
			i += 2
			continue
		case "||":
			tokens = append(tokens, exprToken{kind: tokOr, text: "||"})
			i += 2
			continue
		}

		switch ch {
		case '+':
			tokens = append(tokens, exprToken{kind: tokPlus, text: "+"})
		case '-':
			tokens = append(tokens, exprToken{kind: tokMinus, text: "-"})
		case '*':
			tokens = append(tokens, exprToken{kind: tokMul, text: "*"})
		case '/':
			tokens = append(tokens, exprToken{kind: tokDiv, text: "/"})
		case '>':
			tokens = append(tokens, exprToken{kind: tokGT, text: ">"})
		case '<':
			tokens = append(tokens, exprToken{kind: tokLT, text: "<"})
		case '!':
			tokens = append(tokens, exprToken{kind: tokNot, text: "!"})
		case '(':
			tokens = append(tokens, exprToken{kind: tokLParen, text: "("})
		case ')':
			tokens = append(tokens, exprToken{kind: tokRParen, text: ")"})
		case ',':
			tokens = append(tokens, exprToken{kind: tokComma, text: ","})
		default:
			return nil, fmt.Errorf("unexpected character %q in expression", ch)
		}
		i++
	}
	tokens = append(tokens, exprToken{kind: tokEOF})
	return tokens, nil
}

func scanStringLiteral(expr string, start int) (string, int, error) {
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

func scanNumberLiteral(expr string, start int) (float64, string, int, error) {
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
	if i < len(expr) && (isIdentStart(expr[i]) || expr[i] == '.') {
		return 0, "", 0, fmt.Errorf("invalid numeric literal near %q", expr[start:i+1])
	}
	raw := expr[start:i]
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, "", 0, fmt.Errorf("invalid numeric literal %q", raw)
	}
	return val, raw, i, nil
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentContinue(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

type exprParser struct {
	tokens     []exprToken
	pos        int
	nodes      int
	parenDepth int
	usedIdent  map[string]struct{}
}

func (p *exprParser) peek() exprToken {
	if p.pos >= len(p.tokens) {
		return exprToken{kind: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *exprParser) advance() exprToken {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *exprParser) newNode(node exprNode) (*exprNode, error) {
	p.nodes++
	if p.nodes > MaxExpressionASTNodes {
		return nil, fmt.Errorf("expression exceeds maximum AST node count (%d)", MaxExpressionASTNodes)
	}
	return &node, nil
}

func (p *exprParser) parseOr() (*exprNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokOr {
		op := p.advance().kind
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(exprNode{kind: nodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *exprParser) parseAnd() (*exprNode, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokAnd {
		op := p.advance().kind
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(exprNode{kind: nodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *exprParser) parseEquality() (*exprNode, error) {
	left, err := p.parseRelational()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokEQ || p.peek().kind == tokNE {
		op := p.advance().kind
		right, err := p.parseRelational()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(exprNode{kind: nodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *exprParser) parseRelational() (*exprNode, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	for {
		kind := p.peek().kind
		if kind != tokGT && kind != tokGTE && kind != tokLT && kind != tokLTE {
			break
		}
		p.advance()
		right, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(exprNode{kind: nodeBinary, op: kind, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *exprParser) parseAdditive() (*exprNode, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokPlus || p.peek().kind == tokMinus {
		op := p.advance().kind
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(exprNode{kind: nodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *exprParser) parseMultiplicative() (*exprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokMul || p.peek().kind == tokDiv {
		op := p.advance().kind
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left, err = p.newNode(exprNode{kind: nodeBinary, op: op, left: left, right: right})
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

func (p *exprParser) parseUnary() (*exprNode, error) {
	if p.peek().kind == tokNot || p.peek().kind == tokMinus {
		op := p.advance().kind
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return p.newNode(exprNode{kind: nodeUnary, op: op, left: operand})
	}
	return p.parsePrimary()
}

func (p *exprParser) parsePrimary() (*exprNode, error) {
	tok := p.peek()
	switch tok.kind {
	case tokNumber:
		p.advance()
		return p.newNode(exprNode{kind: nodeNumber, number: tok.number})
	case tokBool:
		p.advance()
		return p.newNode(exprNode{kind: nodeBool, boolean: tok.boolean})
	case tokString:
		p.advance()
		return p.newNode(exprNode{kind: nodeString, str: tok.text})
	case tokIdent:
		p.advance()
		if tok.text == "abs" || tok.text == "min" || tok.text == "max" {
			if p.peek().kind != tokLParen {
				return nil, fmt.Errorf("reserved keyword %q cannot be used as identifier", tok.text)
			}
			return p.parseCall(tok.text)
		}
		if p.peek().kind == tokLParen {
			return nil, fmt.Errorf("unknown expression function %q", tok.text)
		}
		p.usedIdent[tok.text] = struct{}{}
		return p.newNode(exprNode{kind: nodeIdent, str: tok.text})
	case tokLParen:
		p.advance()
		p.parenDepth++
		if p.parenDepth > MaxExpressionDepth {
			return nil, fmt.Errorf("expression nesting depth exceeds maximum %d", MaxExpressionDepth)
		}
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, errors.New("expected ')' in expression")
		}
		p.advance()
		p.parenDepth--
		inner.grouped = true
		return inner, nil
	default:
		if tok.kind == tokEOF {
			return nil, errors.New("unexpected end of expression")
		}
		return nil, fmt.Errorf("unexpected token %q in expression", tok.text)
	}
}

func (p *exprParser) parseCall(name string) (*exprNode, error) {
	p.advance() // '('
	p.parenDepth++
	if p.parenDepth > MaxExpressionDepth {
		return nil, fmt.Errorf("expression nesting depth exceeds maximum %d", MaxExpressionDepth)
	}
	if p.peek().kind == tokRParen {
		return nil, fmt.Errorf("function %s requires arguments", name)
	}
	args := make([]*exprNode, 0, 2)
	for {
		arg, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.peek().kind != tokComma {
			break
		}
		p.advance()
	}
	if p.peek().kind != tokRParen {
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
	return p.newNode(exprNode{kind: nodeCall, str: name, args: args})
}

func astDepth(node *exprNode) int {
	if node == nil {
		return 0
	}
	if node.kind == nodeBinary {
		leftDepth := astDepth(node.left)
		// Unparenthesized left-associative chains of the same operator (e.g. a && b && ... && p)
		// represent a flat list rather than nested levels.
		if node.left != nil && !node.left.grouped && node.left.kind == nodeBinary && node.left.op == node.op {
			leftDepth--
		}
		rightDepth := astDepth(node.right)
		best := leftDepth
		if rightDepth > best {
			best = rightDepth
		}
		return 1 + best
	}
	best := 0
	if d := astDepth(node.left); d > best {
		best = d
	}
	if d := astDepth(node.right); d > best {
		best = d
	}
	for _, arg := range node.args {
		if d := astDepth(arg); d > best {
			best = d
		}
	}
	return 1 + best
}

func checkExprNodeType(node *exprNode) (exprValueType, error) {
	switch node.kind {
	case nodeNumber:
		return exprTypeNumber, nil
	case nodeBool:
		return exprTypeBoolean, nil
	case nodeString:
		return exprTypeString, nil
	case nodeIdent:
		return exprTypeAny, nil
	case nodeUnary:
		operandType, err := checkExprNodeType(node.left)
		if err != nil {
			return exprTypeAny, err
		}
		if node.op == tokNot {
			if operandType != exprTypeBoolean && operandType != exprTypeAny {
				return exprTypeAny, errors.New("logical '!' requires a boolean operand")
			}
			return exprTypeBoolean, nil
		}
		if operandType != exprTypeNumber && operandType != exprTypeAny {
			return exprTypeAny, errors.New("unary '-' requires a numeric operand")
		}
		return exprTypeNumber, nil
	case nodeCall:
		for _, arg := range node.args {
			argType, err := checkExprNodeType(arg)
			if err != nil {
				return exprTypeAny, err
			}
			if argType != exprTypeNumber && argType != exprTypeAny {
				return exprTypeAny, fmt.Errorf("function %s requires numeric arguments", node.str)
			}
		}
		return exprTypeNumber, nil
	case nodeBinary:
		leftType, err := checkExprNodeType(node.left)
		if err != nil {
			return exprTypeAny, err
		}
		rightType, err := checkExprNodeType(node.right)
		if err != nil {
			return exprTypeAny, err
		}
		switch node.op {
		case tokPlus, tokMinus, tokMul, tokDiv:
			if (leftType != exprTypeNumber && leftType != exprTypeAny) || (rightType != exprTypeNumber && rightType != exprTypeAny) {
				return exprTypeAny, errors.New("arithmetic operator requires numeric operands")
			}
			return exprTypeNumber, nil
		case tokGT, tokGTE, tokLT, tokLTE:
			if (leftType != exprTypeNumber && leftType != exprTypeAny) || (rightType != exprTypeNumber && rightType != exprTypeAny) {
				return exprTypeAny, errors.New("ordered comparison operator requires numeric operands")
			}
			return exprTypeBoolean, nil
		case tokAnd, tokOr:
			if (leftType != exprTypeBoolean && leftType != exprTypeAny) || (rightType != exprTypeBoolean && rightType != exprTypeAny) {
				return exprTypeAny, errors.New("logical operator requires boolean operands")
			}
			return exprTypeBoolean, nil
		case tokEQ, tokNE:
			if leftType != exprTypeAny && rightType != exprTypeAny && leftType != rightType {
				return exprTypeAny, errors.New("equality operator compares incompatible types")
			}
			return exprTypeBoolean, nil
		}
	}
	return exprTypeAny, errors.New("unsupported expression AST node")
}

type exprEvalResult struct {
	indeterminate bool
	blocker       string
	sourceKey     string
	value         TypedValue
	nextDue       *time.Time
}

func evaluateExpression(policy AlarmPolicyRevision, condition Condition, snapshot EvaluationSnapshot, now time.Time) predicateResult {
	root, err := parseAndValidateExpression(condition.Expression, condition.ExprInputs)
	if err != nil {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + "EXPRESSION"}
	}
	result := evalExprNode(root, policy, condition.ExprInputs, snapshot, now)
	if result.indeterminate {
		return predicateResult{status: EvaluationIndeterminate, blocker: result.blocker, nextDue: result.nextDue}
	}
	if result.value.Type != EvaluationValueBoolean {
		return predicateResult{status: EvaluationIndeterminate, blocker: invalidExprBlocker(result.sourceKey), nextDue: result.nextDue}
	}
	return truthResult(result.value.Boolean, result.nextDue)
}

func evalExprNode(node *exprNode, policy AlarmPolicyRevision, inputs map[string]string, snapshot EvaluationSnapshot, now time.Time) exprEvalResult {
	switch node.kind {
	case nodeNumber:
		return exprEvalResult{value: NumberValue(node.number)}
	case nodeBool:
		return exprEvalResult{value: BooleanValue(node.boolean)}
	case nodeString:
		return exprEvalResult{value: StringValue(node.str)}
	case nodeIdent:
		inputKey := inputs[node.str]
		fact, exists := snapshot.Inputs[inputKey]
		trusted, blocker, nextDue := trustedInput(policy, inputKey, fact, exists, now)
		if !trusted {
			return exprEvalResult{indeterminate: true, blocker: blocker, sourceKey: inputKey, nextDue: nextDue}
		}
		if fact.Value.Type == EvaluationValueNumber && (math.IsNaN(fact.Value.Number) || math.IsInf(fact.Value.Number, 0)) {
			return exprEvalResult{indeterminate: true, blocker: qualityBlockerInvalidPrefix + inputKey, sourceKey: inputKey, nextDue: nextDue}
		}
		return exprEvalResult{value: fact.Value, sourceKey: inputKey, nextDue: nextDue}
	case nodeUnary:
		operand := evalExprNode(node.left, policy, inputs, snapshot, now)
		if operand.indeterminate {
			return operand
		}
		if node.op == tokNot {
			if operand.value.Type != EvaluationValueBoolean {
				return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(operand.sourceKey), nextDue: operand.nextDue}
			}
			return exprEvalResult{value: BooleanValue(!operand.value.Boolean), nextDue: operand.nextDue}
		}
		if operand.value.Type != EvaluationValueNumber {
			return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(operand.sourceKey), nextDue: operand.nextDue}
		}
		val := -operand.value.Number
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return exprEvalResult{indeterminate: true, blocker: qualityBlockerInvalidPrefix + "NON_FINITE", nextDue: operand.nextDue}
		}
		return exprEvalResult{value: NumberValue(val), nextDue: operand.nextDue}
	case nodeCall:
		values := make([]float64, 0, len(node.args))
		var nextDue *time.Time
		for _, arg := range node.args {
			evaluated := evalExprNode(arg, policy, inputs, snapshot, now)
			if evaluated.indeterminate {
				return evaluated
			}
			if evaluated.value.Type != EvaluationValueNumber {
				return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(evaluated.sourceKey), nextDue: minTime(nextDue, evaluated.nextDue)}
			}
			nextDue = minTime(nextDue, evaluated.nextDue)
			values = append(values, evaluated.value.Number)
		}
		var result float64
		switch node.str {
		case "abs":
			result = math.Abs(values[0])
		case "min":
			result = values[0]
			for _, v := range values[1:] {
				if v < result {
					result = v
				}
			}
		case "max":
			result = values[0]
			for _, v := range values[1:] {
				if v > result {
					result = v
				}
			}
		}
		if math.IsNaN(result) || math.IsInf(result, 0) {
			return exprEvalResult{indeterminate: true, blocker: qualityBlockerInvalidPrefix + "NON_FINITE", nextDue: nextDue}
		}
		return exprEvalResult{value: NumberValue(result), nextDue: nextDue}
	case nodeBinary:
		switch node.op {
		case tokAnd:
			return evalKleeneAnd(node.left, node.right, policy, inputs, snapshot, now)
		case tokOr:
			return evalKleeneOr(node.left, node.right, policy, inputs, snapshot, now)
		}
		left := evalExprNode(node.left, policy, inputs, snapshot, now)
		if left.indeterminate {
			return left
		}
		right := evalExprNode(node.right, policy, inputs, snapshot, now)
		if right.indeterminate {
			return right
		}
		nextDue := minTime(left.nextDue, right.nextDue)
		switch node.op {
		case tokPlus, tokMinus, tokMul, tokDiv:
			if left.value.Type != EvaluationValueNumber {
				return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(left.sourceKey), nextDue: nextDue}
			}
			if right.value.Type != EvaluationValueNumber {
				return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(right.sourceKey), nextDue: nextDue}
			}
			if node.op == tokDiv && math.Abs(right.value.Number) < exprDivZeroEpsilon {
				return exprEvalResult{indeterminate: true, blocker: qualityBlockerInvalidPrefix + "DIV_BY_ZERO", nextDue: nextDue}
			}
			var res float64
			switch node.op {
			case tokPlus:
				res = left.value.Number + right.value.Number
			case tokMinus:
				res = left.value.Number - right.value.Number
			case tokMul:
				res = left.value.Number * right.value.Number
			case tokDiv:
				res = left.value.Number / right.value.Number
			}
			if math.IsNaN(res) || math.IsInf(res, 0) {
				return exprEvalResult{indeterminate: true, blocker: qualityBlockerInvalidPrefix + "NON_FINITE", nextDue: nextDue}
			}
			return exprEvalResult{value: NumberValue(res), nextDue: nextDue}
		case tokGT, tokGTE, tokLT, tokLTE:
			if left.value.Type != EvaluationValueNumber {
				return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(left.sourceKey), nextDue: nextDue}
			}
			if right.value.Type != EvaluationValueNumber {
				return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(right.sourceKey), nextDue: nextDue}
			}
			return exprEvalResult{value: BooleanValue(compareTyped(left.value, tokenToCompareOperator(node.op), right.value)), nextDue: nextDue}
		case tokEQ, tokNE:
			if left.value.Type != right.value.Type {
				sourceKey := left.sourceKey
				if sourceKey == "" {
					sourceKey = right.sourceKey
				}
				return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(sourceKey), nextDue: nextDue}
			}
			return exprEvalResult{value: BooleanValue(compareTyped(left.value, tokenToCompareOperator(node.op), right.value)), nextDue: nextDue}
		}
	}
	return exprEvalResult{indeterminate: true, blocker: qualityBlockerInvalidPrefix + "EXPRESSION"}
}

func evalKleeneAnd(leftNode, rightNode *exprNode, policy AlarmPolicyRevision, inputs map[string]string, snapshot EvaluationSnapshot, now time.Time) exprEvalResult {
	left := evalExprNode(leftNode, policy, inputs, snapshot, now)
	if !left.indeterminate {
		if left.value.Type != EvaluationValueBoolean {
			return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(left.sourceKey), nextDue: left.nextDue}
		}
		if !left.value.Boolean {
			return exprEvalResult{value: BooleanValue(false), nextDue: left.nextDue}
		}
	}
	right := evalExprNode(rightNode, policy, inputs, snapshot, now)
	if !right.indeterminate {
		if right.value.Type != EvaluationValueBoolean {
			return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(right.sourceKey), nextDue: minTime(left.nextDue, right.nextDue)}
		}
		if !right.value.Boolean {
			return exprEvalResult{value: BooleanValue(false), nextDue: right.nextDue}
		}
	}
	if left.indeterminate {
		return exprEvalResult{indeterminate: true, blocker: left.blocker, nextDue: minTime(left.nextDue, right.nextDue)}
	}
	if right.indeterminate {
		return exprEvalResult{indeterminate: true, blocker: right.blocker, nextDue: minTime(left.nextDue, right.nextDue)}
	}
	return exprEvalResult{value: BooleanValue(true), nextDue: minTime(left.nextDue, right.nextDue)}
}

func evalKleeneOr(leftNode, rightNode *exprNode, policy AlarmPolicyRevision, inputs map[string]string, snapshot EvaluationSnapshot, now time.Time) exprEvalResult {
	left := evalExprNode(leftNode, policy, inputs, snapshot, now)
	if !left.indeterminate {
		if left.value.Type != EvaluationValueBoolean {
			return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(left.sourceKey), nextDue: left.nextDue}
		}
		if left.value.Boolean {
			return exprEvalResult{value: BooleanValue(true), nextDue: left.nextDue}
		}
	}
	right := evalExprNode(rightNode, policy, inputs, snapshot, now)
	if !right.indeterminate {
		if right.value.Type != EvaluationValueBoolean {
			return exprEvalResult{indeterminate: true, blocker: invalidExprBlocker(right.sourceKey), nextDue: minTime(left.nextDue, right.nextDue)}
		}
		if right.value.Boolean {
			return exprEvalResult{value: BooleanValue(true), nextDue: right.nextDue}
		}
	}
	if left.indeterminate {
		return exprEvalResult{indeterminate: true, blocker: left.blocker, nextDue: minTime(left.nextDue, right.nextDue)}
	}
	if right.indeterminate {
		return exprEvalResult{indeterminate: true, blocker: right.blocker, nextDue: minTime(left.nextDue, right.nextDue)}
	}
	return exprEvalResult{value: BooleanValue(false), nextDue: minTime(left.nextDue, right.nextDue)}
}

func tokenToCompareOperator(kind exprTokenKind) CompareOperator {
	switch kind {
	case tokEQ:
		return CompareEQ
	case tokNE:
		return CompareNE
	case tokGT:
		return CompareGT
	case tokGTE:
		return CompareGTE
	case tokLT:
		return CompareLT
	case tokLTE:
		return CompareLTE
	default:
		return ""
	}
}

func invalidExprBlocker(sourceKey string) string {
	if sourceKey != "" {
		return qualityBlockerInvalidPrefix + sourceKey
	}
	return qualityBlockerInvalidPrefix + "TYPE_MISMATCH"
}
