package expression

import (
	"fmt"

	"github.com/google/cel-go/cel"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// Loom adapter: compile with the same environments as the official evaluator,
// then lower only AST constructs whose Argo expr semantics are equivalent.
func (e *Expr) CompileCondition(source string) (*exprpb.Expr, error) {
	ast, issues := e.conditionEnv.Compile(source)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	if ast == nil {
		return nil, fmt.Errorf("empty condition AST")
	}
	if !ast.OutputType().IsExactType(cel.BoolType) {
		return nil, fmt.Errorf("condition must return bool")
	}
	return ast.Expr(), nil
}
func (e *Expr) CompileSelector(source string) (*exprpb.Expr, error) {
	ast, issues := e.selectEnv.Compile(source)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	if ast == nil {
		return nil, fmt.Errorf("empty selector AST")
	}
	return ast.Expr(), nil
}
