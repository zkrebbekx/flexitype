package core

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Expression is a simple expression evaluator for dynamic cascade logic
type Expression struct {
	Expression string
}

// NewExpression creates a new expression evaluator
func NewExpression(expr string) *Expression {
	return &Expression{
		Expression: expr,
	}
}

// Token types for parsing
type TokenType int

const (
	TokenAnd TokenType = iota
	TokenOr
	TokenNot
	TokenOpenParen
	TokenCloseParen
	TokenEqual
	TokenNotEqual
	TokenGreaterThan
	TokenLessThan
	TokenGreaterEqual
	TokenLessEqual
	TokenIsEmpty
	TokenIsNotEmpty
	TokenOperand
)

// Token represents a token in the expression
type Token struct {
	Type  TokenType
	Value string
}

// Evaluate evaluates the expression in the context of the given instance
func (e *Expression) Evaluate(instance *Instance) (bool, error) {
	if e.Expression == "" {
		return true, nil // Empty expression evaluates to true
	}

	// Check if the expression contains a consequence part
	if strings.Contains(e.Expression, "=>") {
		// For expressions with consequences, we only evaluate the condition part
		parts := strings.Split(e.Expression, "=>")
		if len(parts) < 1 {
			return false, fmt.Errorf("invalid expression format: %s", e.Expression)
		}

		condition := strings.TrimSpace(parts[0])
		return e.evaluateComplexExpression(condition, instance)
	}

	// For expressions without consequences, evaluate the whole expression
	return e.evaluateComplexExpression(e.Expression, instance)
}

// evaluateComplexExpression evaluates a complex expression with multiple operators and parentheses
func (e *Expression) evaluateComplexExpression(expr string, instance *Instance) (bool, error) {
	// Tokenize the expression
	tokens, err := e.tokenize(expr)
	if err != nil {
		return false, err
	}

	// Handle simple case - single condition
	if len(tokens) == 1 {
		return e.evaluateCondition(tokens[0].Value, instance)
	}

	// Parse and evaluate the expression using recursive descent parsing
	result, _, err := e.parseExpression(tokens, 0, instance)
	if err != nil {
		return false, err
	}

	return result, nil
}

// tokenize breaks an expression into tokens
func (e *Expression) tokenize(expr string) ([]Token, error) {
	tokens := []Token{}

	// Add spaces around operators and parentheses for easier tokenization
	expr = strings.ReplaceAll(expr, "(", " ( ")
	expr = strings.ReplaceAll(expr, ")", " ) ")
	expr = strings.ReplaceAll(expr, "&&", " && ")
	expr = strings.ReplaceAll(expr, "||", " || ")
	expr = strings.ReplaceAll(expr, ">=", " >= ")
	expr = strings.ReplaceAll(expr, "<=", " <= ")
	expr = strings.ReplaceAll(expr, "!=", " != ")
	expr = strings.ReplaceAll(expr, "==", " == ")
	expr = strings.ReplaceAll(expr, ">", " > ")
	expr = strings.ReplaceAll(expr, "<", " < ")
	expr = strings.ReplaceAll(expr, "!", " ! ")

	// Special handling for "isEmpty" and "isNotEmpty" functions
	expr = strings.ReplaceAll(expr, "isEmpty(", " isEmpty ( ")
	expr = strings.ReplaceAll(expr, "isNotEmpty(", " isNotEmpty ( ")

	// Split by whitespace
	parts := strings.Fields(expr)

	// Process each part
	for i := 0; i < len(parts); i++ {
		part := parts[i]

		switch part {
		case "&&":
			tokens = append(tokens, Token{Type: TokenAnd, Value: part})
		case "||":
			tokens = append(tokens, Token{Type: TokenOr, Value: part})
		case "!":
			tokens = append(tokens, Token{Type: TokenNot, Value: part})
		case "(":
			tokens = append(tokens, Token{Type: TokenOpenParen, Value: part})
		case ")":
			tokens = append(tokens, Token{Type: TokenCloseParen, Value: part})
		case "==":
			tokens = append(tokens, Token{Type: TokenEqual, Value: part})
		case "!=":
			tokens = append(tokens, Token{Type: TokenNotEqual, Value: part})
		case ">":
			tokens = append(tokens, Token{Type: TokenGreaterThan, Value: part})
		case "<":
			tokens = append(tokens, Token{Type: TokenLessThan, Value: part})
		case ">=":
			tokens = append(tokens, Token{Type: TokenGreaterEqual, Value: part})
		case "<=":
			tokens = append(tokens, Token{Type: TokenLessEqual, Value: part})
		case "isEmpty":
			// For isEmpty function, we need to collect the attribute name
			if i+2 < len(parts) && parts[i+1] == "(" && parts[i+2] != ")" {
				tokens = append(tokens, Token{Type: TokenIsEmpty, Value: parts[i+2]})
				i += 3 // Skip the '(' and attribute and ')'
			} else {
				return nil, fmt.Errorf("invalid isEmpty syntax at position %d", i)
			}
		case "isNotEmpty":
			// For isNotEmpty function, we need to collect the attribute name
			if i+2 < len(parts) && parts[i+1] == "(" && parts[i+2] != ")" {
				tokens = append(tokens, Token{Type: TokenIsNotEmpty, Value: parts[i+2]})
				i += 3 // Skip the '(' and attribute and ')'
			} else {
				return nil, fmt.Errorf("invalid isNotEmpty syntax at position %d", i)
			}
		default:
			// This is an operand (attribute name or literal value)
			tokens = append(tokens, Token{Type: TokenOperand, Value: part})
		}
	}

	return tokens, nil
}

// parseExpression parses and evaluates a tokenized expression
func (e *Expression) parseExpression(tokens []Token, pos int, instance *Instance) (bool, int, error) {
	if pos >= len(tokens) {
		return false, pos, fmt.Errorf("unexpected end of expression")
	}

	// Parse the first term
	result, newPos, err := e.parseTerm(tokens, pos, instance)
	if err != nil {
		return false, newPos, err
	}

	pos = newPos

	// Parse operators (OR has lowest precedence)
	for pos < len(tokens) && tokens[pos].Type == TokenOr {
		// Skip the OR token
		pos++

		// Parse the next term
		rightResult, newPos, err := e.parseTerm(tokens, pos, instance)
		if err != nil {
			return false, newPos, err
		}

		// Evaluate the OR operation
		result = result || rightResult
		pos = newPos
	}

	return result, pos, nil
}

// parseTerm parses and evaluates a term (parts connected by AND)
func (e *Expression) parseTerm(tokens []Token, pos int, instance *Instance) (bool, int, error) {
	if pos >= len(tokens) {
		return false, pos, fmt.Errorf("unexpected end of term")
	}

	// Parse the first factor
	result, newPos, err := e.parseFactor(tokens, pos, instance)
	if err != nil {
		return false, newPos, err
	}

	pos = newPos

	// Parse operators (AND has higher precedence than OR)
	for pos < len(tokens) && tokens[pos].Type == TokenAnd {
		// Skip the AND token
		pos++

		// Parse the next factor
		rightResult, newPos, err := e.parseFactor(tokens, pos, instance)
		if err != nil {
			return false, newPos, err
		}

		// Evaluate the AND operation
		result = result && rightResult
		pos = newPos
	}

	return result, pos, nil
}

// parseFactor parses and evaluates a factor (atomic expression or parenthesized expression)
func (e *Expression) parseFactor(tokens []Token, pos int, instance *Instance) (bool, int, error) {
	if pos >= len(tokens) {
		return false, pos, fmt.Errorf("unexpected end of factor")
	}

	token := tokens[pos]

	// Handle NOT operator
	if token.Type == TokenNot {
		result, newPos, err := e.parseFactor(tokens, pos+1, instance)
		if err != nil {
			return false, newPos, err
		}
		return !result, newPos, nil
	}

	// Handle parenthesized expression
	if token.Type == TokenOpenParen {
		result, newPos, err := e.parseExpression(tokens, pos+1, instance)
		if err != nil {
			return false, newPos, err
		}

		// Ensure we have a closing parenthesis
		if newPos >= len(tokens) || tokens[newPos].Type != TokenCloseParen {
			return false, newPos, fmt.Errorf("missing closing parenthesis")
		}

		return result, newPos + 1, nil
	}

	// Handle isEmpty
	if token.Type == TokenIsEmpty {
		// Check if the attribute exists and is empty
		attrName := token.Value
		value, err := instance.GetAttribute(attrName)
		if err != nil || value == nil {
			return true, pos + 1, nil // Attribute doesn't exist or is nil
		}

		// Check if the value is empty
		isEmpty, err := isEmptyValue(value)
		if err != nil {
			return false, pos, err
		}

		return isEmpty, pos + 1, nil
	}

	// Handle isNotEmpty
	if token.Type == TokenIsNotEmpty {
		// Check if the attribute exists and is not empty
		attrName := token.Value
		value, err := instance.GetAttribute(attrName)
		if err != nil || value == nil {
			return false, pos + 1, nil // Attribute doesn't exist or is nil
		}

		// Check if the value is empty
		isEmpty, err := isEmptyValue(value)
		if err != nil {
			return false, pos, err
		}

		return !isEmpty, pos + 1, nil
	}

	// Handle comparison operators
	if token.Type == TokenOperand {
		// We need at least 3 tokens for a comparison: left operand, operator, right operand
		if pos+2 >= len(tokens) {
			return false, pos, fmt.Errorf("incomplete comparison expression")
		}

		leftOperand := token.Value
		operator := tokens[pos+1]
		rightOperand := tokens[pos+2]

		// Check if we have a valid comparison operator
		if operator.Type >= TokenEqual && operator.Type <= TokenLessEqual {
			// Get left operand value (always an attribute)
			leftValue, err := instance.GetAttribute(leftOperand)
			if err != nil {
				return false, pos, fmt.Errorf("error getting attribute %s: %w", leftOperand, err)
			}

			// Get right operand value (could be a literal or an attribute)
			var rightValue interface{}

			// Check if it's a literal value
			rightValue, err = parseLiteral(rightOperand.Value)
			if err != nil {
				// Not a literal, try as an attribute
				rightValue, err = instance.GetAttribute(rightOperand.Value)
				if err != nil {
					return false, pos, fmt.Errorf("invalid right operand: %s", rightOperand.Value)
				}
			}

			// Perform the comparison
			result, err := compareValues(leftValue, rightValue, operator.Type)
			if err != nil {
				return false, pos, err
			}

			return result, pos + 3, nil
		}
	}

	return false, pos, fmt.Errorf("invalid expression at position %d", pos)
}

// parseLiteral tries to parse a string as a literal value (number, boolean, string)
func parseLiteral(value string) (interface{}, error) {
	// Try as number
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f, nil
	}

	// Try as boolean
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}

	// Check if it's a quoted string
	if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
		(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
		// Remove the quotes
		return value[1 : len(value)-1], nil
	}

	// Not a literal
	return nil, fmt.Errorf("not a literal value: %s", value)
}

// isEmptyValue checks if a value is considered "empty"
func isEmptyValue(value interface{}) (bool, error) {
	if value == nil {
		return true, nil
	}

	v := reflect.ValueOf(value)

	switch v.Kind() {
	case reflect.String:
		return v.String() == "", nil
	case reflect.Slice, reflect.Map, reflect.Array:
		return v.Len() == 0, nil
	case reflect.Bool:
		return !v.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0, nil
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0, nil
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return true, nil
		}
		return isEmptyValue(v.Elem().Interface())
	default:
		// For other types, not sure how to determine emptiness
		return false, nil
	}
}

// compareValues compares two values based on the operator
func compareValues(left, right interface{}, operator TokenType) (bool, error) {
	switch operator {
	case TokenEqual:
		return compareEquals(left, right)
	case TokenNotEqual:
		eq, err := compareEquals(left, right)
		return !eq, err
	case TokenGreaterThan:
		return compareGreaterThan(left, right)
	case TokenLessThan:
		gt, err := compareGreaterThan(right, left) // Reversed for less than
		return gt, err
	case TokenGreaterEqual:
		gt, err := compareGreaterThan(left, right)
		if err != nil {
			return false, err
		}
		eq, err := compareEquals(left, right)
		return gt || eq, err
	case TokenLessEqual:
		gt, err := compareGreaterThan(left, right)
		if err != nil {
			return false, err
		}
		return !gt, err
	default:
		return false, fmt.Errorf("unsupported operator: %v", operator)
	}
}

// evaluateCondition evaluates a single condition like "amount > 100"
func (e *Expression) evaluateCondition(condition string, instance *Instance) (bool, error) {
	condition = strings.TrimSpace(condition)

	// Check for different comparison operators
	var operator string
	var parts []string

	if strings.Contains(condition, ">=") {
		parts = strings.Split(condition, ">=")
		operator = ">="
	} else if strings.Contains(condition, "<=") {
		parts = strings.Split(condition, "<=")
		operator = "<="
	} else if strings.Contains(condition, "!=") {
		parts = strings.Split(condition, "!=")
		operator = "!="
	} else if strings.Contains(condition, "==") {
		parts = strings.Split(condition, "==")
		operator = "=="
	} else if strings.Contains(condition, ">") {
		parts = strings.Split(condition, ">")
		operator = ">"
	} else if strings.Contains(condition, "<") {
		parts = strings.Split(condition, "<")
		operator = "<"
	} else {
		return false, fmt.Errorf("invalid condition format: %s", condition)
	}

	if len(parts) != 2 {
		return false, fmt.Errorf("invalid condition format: %s", condition)
	}

	leftSide := strings.TrimSpace(parts[0])
	rightSide := strings.TrimSpace(parts[1])

	// Get left side value (always an attribute)
	leftValue, err := instance.GetAttribute(leftSide)
	if err != nil {
		return false, fmt.Errorf("error getting attribute %s: %w", leftSide, err)
	}

	// Get right side value (could be a literal or an attribute)
	var rightValue interface{}

	// First see if right side is a numeric literal
	if f, err := strconv.ParseFloat(rightSide, 64); err == nil {
		rightValue = f
	} else if rightSide == "true" {
		rightValue = true
	} else if rightSide == "false" {
		rightValue = false
	} else if strings.HasPrefix(rightSide, "\"") && strings.HasSuffix(rightSide, "\"") {
		// String literal
		rightValue = rightSide[1 : len(rightSide)-1]
	} else {
		// Try as an attribute
		rightValue, err = instance.GetAttribute(rightSide)
		if err != nil {
			return false, fmt.Errorf("invalid right-side value: %s", rightSide)
		}
	}

	// Compare based on operator and types
	switch operator {
	case "==":
		return compareEquals(leftValue, rightValue)
	case "!=":
		equals, err := compareEquals(leftValue, rightValue)
		return !equals, err
	case ">":
		return compareGreaterThan(leftValue, rightValue)
	case ">=":
		gt, err := compareGreaterThan(leftValue, rightValue)
		if err != nil {
			return false, err
		}
		eq, err := compareEquals(leftValue, rightValue)
		return gt || eq, err
	case "<":
		gt, err := compareGreaterThan(leftValue, rightValue)
		if err != nil {
			return false, err
		}
		eq, err := compareEquals(leftValue, rightValue)
		return !gt && !eq, err
	case "<=":
		gt, err := compareGreaterThan(leftValue, rightValue)
		if err != nil {
			return false, err
		}
		return !gt, err
	default:
		return false, fmt.Errorf("unsupported operator: %s", operator)
	}
}

// compareEquals compares two values for equality
func compareEquals(a, b interface{}) (bool, error) {
	// Try to convert both to same type for comparison
	aFloat, aIsFloat := a.(float64)
	bFloat, bIsFloat := b.(float64)

	if aIsFloat && bIsFloat {
		return aFloat == bFloat, nil
	}

	// Try as strings
	aStr, aIsStr := a.(string)
	bStr, bIsStr := b.(string)

	if aIsStr && bIsStr {
		return aStr == bStr, nil
	}

	// Try as booleans
	aBool, aIsBool := a.(bool)
	bBool, bIsBool := b.(bool)

	if aIsBool && bIsBool {
		return aBool == bBool, nil
	}

	// If types don't match, try to convert
	if aIsFloat && bIsStr {
		bFloatVal, err := strconv.ParseFloat(bStr, 64)
		if err == nil {
			return aFloat == bFloatVal, nil
		}
	} else if aIsStr && bIsFloat {
		aFloatVal, err := strconv.ParseFloat(aStr, 64)
		if err == nil {
			return aFloatVal == bFloat, nil
		}
	}

	// Last resort, string comparison
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b), nil
}

// compareGreaterThan compares if a > b
func compareGreaterThan(a, b interface{}) (bool, error) {
	// Try to convert both to float for comparison
	aFloat, aIsFloat := a.(float64)
	bFloat, bIsFloat := b.(float64)

	if aIsFloat && bIsFloat {
		return aFloat > bFloat, nil
	}

	// Try to convert string to float
	aStr, aIsStr := a.(string)
	if aIsStr {
		var err error
		aFloat, err = strconv.ParseFloat(aStr, 64)
		if err != nil {
			return false, fmt.Errorf("cannot compare %v > %v: not comparable types", a, b)
		}
		aIsFloat = true
	}

	bStr, bIsStr := b.(string)
	if bIsStr {
		var err error
		bFloat, err = strconv.ParseFloat(bStr, 64)
		if err != nil {
			return false, fmt.Errorf("cannot compare %v > %v: not comparable types", a, b)
		}
		bIsFloat = true
	}

	if aIsFloat && bIsFloat {
		return aFloat > bFloat, nil
	}

	// String comparison (lexicographic)
	aStr, aIsStr = a.(string)
	bStr, bIsStr = b.(string)
	if aIsStr && bIsStr {
		return aStr > bStr, nil
	}

	return false, fmt.Errorf("cannot compare %v > %v: not comparable types", a, b)
}
