package pipelineir

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	upstream "github.com/githubtiger123/loom/platform/internal/thirdparty/kfp/expression"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

func ExprLiteral(value any) string {
	raw, _ := json.Marshal(value)
	// Literal braces inside strings must not become Argo template delimiters.
	if _, ok := value.(string); ok {
		return strings.NewReplacer("{", `\u007b`, "}", `\u007d`).Replace(string(raw))
	}
	return string(raw)
}

// User expressions never become shell code. CEL is parsed/type-checked by the
// upstream KFP environment; only a bounded, explicit AST subset emits expr.
func ArgoCondition(source, parameters string) (string, error) {
	e, err := upstream.New()
	if err != nil {
		return "", err
	}
	ast, err := e.CompileCondition(source)
	if err != nil {
		return "", fmt.Errorf("invalid CEL condition")
	}
	return lowerCEL(ast, map[string]string{"inputs": "{'parameter_values':" + parameters + "}"}, 0)
}
func ArgoSelector(source, value string) (string, error) {
	if source == "" {
		return value, nil
	}
	e, err := upstream.New()
	if err != nil {
		return "", err
	}
	ast, err := e.CompileSelector(source)
	if err != nil {
		return "", fmt.Errorf("invalid CEL selector")
	}
	// KFP's PbValueToText JSON-serializes list/struct values. expr's string(map)
	// is not JSON, so preserve that special selector behavior explicitly.
	text := "(type(" + value + ") in ['array','map'] ? toJson(" + value + ") : string(" + value + "))"
	return lowerCEL(ast, map[string]string{"string_value": text, "number_value": value, "bool_value": value, "list_value": "{'values':" + value + "}", "struct_value": "{'fields':" + value + "}"}, 0)
}

func lowerCEL(e *exprpb.Expr, variables map[string]string, depth int) (string, error) {
	bad := func() (string, error) {
		return "", fmt.Errorf("CEL expression construct is not supported by the Argo adapter")
	}
	if e == nil || depth > 32 {
		return bad()
	}
	switch n := e.ExprKind.(type) {
	case *exprpb.Expr_IdentExpr:
		v, ok := variables[n.IdentExpr.Name]
		if !ok {
			return bad()
		}
		return "(" + v + ")", nil
	case *exprpb.Expr_ConstExpr:
		switch v := n.ConstExpr.ConstantKind.(type) {
		case *exprpb.Constant_BoolValue:
			return strconv.FormatBool(v.BoolValue), nil
		case *exprpb.Constant_Int64Value:
			return strconv.FormatInt(v.Int64Value, 10), nil
		case *exprpb.Constant_DoubleValue:
			return strconv.FormatFloat(v.DoubleValue, 'g', -1, 64), nil
		case *exprpb.Constant_StringValue:
			return ExprLiteral(v.StringValue), nil
		case *exprpb.Constant_NullValue:
			return "nil", nil
		default:
			return bad()
		}
	case *exprpb.Expr_SelectExpr:
		if n.SelectExpr.TestOnly {
			return bad()
		}
		operand, err := lowerCEL(n.SelectExpr.Operand, variables, depth+1)
		if err != nil {
			return "", err
		}
		return operand + "[" + ExprLiteral(n.SelectExpr.Field) + "]", nil
	case *exprpb.Expr_ListExpr:
		items := []string{}
		for _, item := range n.ListExpr.Elements {
			v, err := lowerCEL(item, variables, depth+1)
			if err != nil {
				return "", err
			}
			items = append(items, v)
		}
		return "[" + strings.Join(items, ",") + "]", nil
	case *exprpb.Expr_CallExpr:
		c := n.CallExpr
		args := []string{}
		if c.Target != nil {
			v, err := lowerCEL(c.Target, variables, depth+1)
			if err != nil {
				return "", err
			}
			args = append(args, v)
		}
		for _, arg := range c.Args {
			v, err := lowerCEL(arg, variables, depth+1)
			if err != nil {
				return "", err
			}
			args = append(args, v)
		}
		binary := map[string]string{"_==_": "==", "_!=_": "!=", "_<_": "<", "_<=_": "<=", "_>_": ">", "_>=_": ">=", "_&&_": "&&", "_||_": "||", "_+_": "+", "_-_": "-", "_*_": "*", "_/_": "/", "_%_": "%", "@in": "in"}
		if op, ok := binary[c.Function]; ok && len(args) == 2 {
			return "(" + args[0] + " " + op + " " + args[1] + ")", nil
		}
		if c.Function == "_[_]" && len(args) == 2 {
			return "(" + args[0] + ")[" + args[1] + "]", nil
		}
		if c.Function == "!_" && len(args) == 1 {
			return "!(" + args[0] + ")", nil
		}
		if c.Function == "-_" && len(args) == 1 {
			return "-(" + args[0] + ")", nil
		}
		if c.Function == "_?_:_" && len(args) == 3 {
			return "(" + args[0] + " ? " + args[1] + " : " + args[2] + ")", nil
		}
		functions := map[string]string{"int": "asInt", "double": "asFloat", "string": "string", "parseJson": "fromJson", "size": "len"}
		if f, ok := functions[c.Function]; ok && len(args) == 1 {
			return f + "(" + args[0] + ")", nil
		}
		if len(args) == 2 {
			switch c.Function {
			case "contains":
				return "(" + args[0] + " contains " + args[1] + ")", nil
			case "startsWith":
				return "sprig.hasPrefix(" + args[1] + "," + args[0] + ")", nil
			case "endsWith":
				return "sprig.hasSuffix(" + args[1] + "," + args[0] + ")", nil
			}
		}
	}
	return bad()
}
