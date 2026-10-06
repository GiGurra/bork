package check

import "github.com/GiGurra/bork/internal/diag"

func (l *lowerer) captureRefs(variables []*Var, at diag.Pos) []Expr {
	var result []Expr
	for _, variable := range variables {
		result = append(result, &VarRef{expr: expr{pos: at, typ: variable.Type}, Var: variable})
	}
	return result
}

func (l *lowerer) captureDictionary(dictionary *Dict, at diag.Pos) *Dict {
	if dictionary == nil {
		return nil
	}
	copy := *dictionary
	copy.Args = nil
	copy.Captures = nil
	for _, declaration := range dictionary.CaptureDecls {
		variable := l.vars[declaration]
		if variable == nil {
			panic("derive dictionary capture has no checked variable")
		}
		copy.Captures = append(copy.Captures, l.captureRefs([]*Var{variable}, at)...)
	}
	for _, argument := range dictionary.Args {
		copy.Args = append(copy.Args, l.captureDictionary(argument, at))
	}
	return &copy
}

func dictionaryCaptureOperands(dictionary *Dict) []Expr {
	if dictionary == nil {
		return nil
	}
	result := append([]Expr(nil), dictionary.Captures...)
	for _, argument := range dictionary.Args {
		result = append(result, dictionaryCaptureOperands(argument)...)
	}
	return result
}

func (l *lowerer) captureInstance(instance *Instance, at diag.Pos) (*Instance, []Expr) {
	if instance == nil {
		return nil, nil
	}
	copy := *instance
	copy.Dicts = nil
	var captures []Expr
	if instance.Func.TemplateScope != nil {
		for i := range instance.Func.TemplateScope.Captures {
			variable := l.vars[&instance.Func.TemplateScope.Captures[i]]
			if variable == nil {
				panic("derive helper capture has no checked variable")
			}
			captures = append(captures, l.captureRefs([]*Var{variable}, at)...)
		}
	}
	for _, dictionary := range instance.Dicts {
		lowered := l.captureDictionary(dictionary, at)
		copy.Dicts = append(copy.Dicts, lowered)
		captures = append(captures, dictionaryCaptureOperands(lowered)...)
	}
	return &copy, captures
}

func (l *lifeChecker) captureLife(captures []Expr) lifetime {
	var result lifetime
	for _, capture := range captures {
		result = result.union(l.use(capture, l.expr(capture)))
	}
	return result
}
