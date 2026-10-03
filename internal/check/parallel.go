package check

import "github.com/GiGurra/bork/internal/diag"

// Until uses a concrete success type to distinguish it from failure values.
// A union erases to any in Go, so its type assertion would accept failures too.
func (c *checker) checkParallelInstance(inst *Instance, pos diag.Pos) bool {
	fn := inst.Func
	if !fn.Prelude || !fn.Decl.IsMethod {
		return true
	}
	var success, failure Type
	switch fn.Decl.Name {
	case "parMapUntil", "parMapUntilIn":
		if len(inst.TypeArgs) != 3 {
			return true
		}
		success, failure = inst.TypeArgs[1], inst.TypeArgs[2]
	case "awaitAllUntil":
		if len(inst.TypeArgs) != 2 {
			return true
		}
		success, failure = inst.TypeArgs[0], inst.TypeArgs[1]
	default:
		return true
	}
	switch success.(type) {
	case *Union, *TypeParam:
		c.errorf(pos, "%s needs a concrete, non-union success type, found %s", fn.Decl.Name, success)
		return false
	}
	if mentionsWhere(success, func(*TypeParam) bool { return true }) || mentionsWhere(failure, func(*TypeParam) bool { return true }) {
		c.errorf(pos, "%s needs concrete success and failure types", fn.Decl.Name)
		return false
	}
	members := []Type{failure}
	if u, ok := failure.(*Union); ok {
		members = u.Members
	}
	for _, m := range members {
		if parallelSameRepresentation(success, m) {
			c.errorf(pos, "%s success type %s overlaps its failure type in Go", fn.Decl.Name, success)
			return false
		}
		if parallelSameRepresentation(&List{Elem: success}, m) {
			c.errorf(pos, "%s result list type List[%s] overlaps its failure type in Go", fn.Decl.Name, success)
			return false
		}
	}
	return true
}

// Effects and unions are erased, including inside generic arguments.
// Check that an assertion to the success type rejects E.
func parallelSameRepresentation(a, b Type) bool {
	switch a := a.(type) {
	case *Union:
		_, ok := b.(*Union)
		return ok
	case *List:
		b, ok := b.(*List)
		return ok && parallelSameRepresentation(a.Elem, b.Elem)
	case *Map:
		b, ok := b.(*Map)
		return ok && parallelSameRepresentation(a.Key, b.Key) && parallelSameRepresentation(a.Value, b.Value)
	case *FuncType:
		b, ok := b.(*FuncType)
		if !ok || len(a.Params) != len(b.Params) || !parallelSameRepresentation(a.Result, b.Result) {
			return false
		}
		for i := range a.Params {
			if !parallelSameRepresentation(a.Params[i], b.Params[i]) {
				return false
			}
		}
		return true
	case *Sealed:
		b, ok := b.(*Sealed)
		if !ok || genericBaseOrSelf(a) != genericBaseOrSelf(b) {
			return false
		}
		aa, ba := TypeArgs(a), TypeArgs(b)
		for i := range aa {
			if !parallelSameRepresentation(aa[i], ba[i]) {
				return false
			}
		}
		return true
	case *Record:
		b, ok := b.(*Record)
		if !ok || genericBaseOrSelf(a) != genericBaseOrSelf(b) {
			return false
		}
		aa, ba := TypeArgs(a), TypeArgs(b)
		for i := range aa {
			if !parallelSameRepresentation(aa[i], ba[i]) {
				return false
			}
		}
		return true
	}
	return identical(a, b)
}
