// Copyright 2020 CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package compile

import (
	"math/big"

	"cuelang.org/go/cue/errors"
	"cuelang.org/go/internal/core/adt"
	"github.com/cockroachdb/apd/v2"
)

// This file contains predeclared builtins.

const supportedByLen = adt.StructKind | adt.BytesKind | adt.StringKind | adt.ListKind

var lenBuiltin = &adt.Builtin{
	Name:   "len",
	Params: []adt.Kind{supportedByLen},
	Result: adt.IntKind,
	Func: func(c *adt.OpContext, args []adt.Value) adt.Expr {
		v := args[0]
		if x, ok := v.(*adt.Vertex); ok {
			switch x.BaseValue.(type) {
			case nil:
				// This should not happen, but be defensive.
				return c.NewErrf("unevaluated vertex")
			case *adt.ListMarker:
				return c.NewInt64(int64(len(x.Elems())), v)

			case *adt.StructMarker:
				n := 0
				v, _ := v.(*adt.Vertex)
				for _, a := range v.Arcs {
					if a.Label.IsRegular() {
						n++
					}
				}
				return c.NewInt64(int64(n), v)

			default:
				v = x.Value()
			}
		}

		switch x := v.(type) {
		case *adt.Bytes:
			return c.NewInt64(int64(len(x.B)), v)
		case *adt.String:
			return c.NewInt64(int64(len(x.Str)), v)
		default:
			k := x.Kind()
			if k&supportedByLen == adt.BottomKind {
				return c.NewErrf("invalid argument type %v", k)
			}
			b := c.NewErrf("incomplete argument %s (type %v)", c.Str(v), k)
			b.Code = adt.IncompleteError
			return b
		}
	},
}

var closeBuiltin = &adt.Builtin{
	Name:   "close",
	Params: []adt.Kind{adt.StructKind},
	Result: adt.StructKind,
	Func: func(c *adt.OpContext, args []adt.Value) adt.Expr {
		s, ok := args[0].(*adt.Vertex)
		if !ok {
			return c.NewErrf("struct argument must be concrete")
		}
		if s.IsClosed(c) {
			return s
		}
		v := *s
		v.BaseValue = &adt.StructMarker{NeedClose: true}
		return &v
	},
}

var andBuiltin = &adt.Builtin{
	Name:   "and",
	Params: []adt.Kind{adt.ListKind},
	Result: adt.IntKind,
	Func: func(c *adt.OpContext, args []adt.Value) adt.Expr {
		list := c.Elems(args[0])
		if len(list) == 0 {
			return &adt.Top{}
		}
		a := []adt.Value{}
		for _, c := range list {
			a = append(a, c)
		}
		return &adt.Conjunction{Values: a}
	},
}

var orBuiltin = &adt.Builtin{
	Name:   "or",
	Params: []adt.Kind{adt.ListKind},
	Result: adt.IntKind,
	Func: func(c *adt.OpContext, args []adt.Value) adt.Expr {
		d := []adt.Disjunct{}
		for _, c := range c.Elems(args[0]) {
			d = append(d, adt.Disjunct{Val: c, Default: false})
		}
		if len(d) == 0 {
			// TODO(manifest): This should not be unconditionally incomplete,
			// but it requires results from comprehensions and all to have
			// some special status. Maybe this can be solved by having results
			// of list comprehensions be open if they result from iterating over
			// an open list or struct. This would actually be exactly what
			// that means. The error here could then only add an incomplete
			// status if the source is open.
			return &adt.Bottom{
				Code: adt.IncompleteError,
				Err:  errors.Newf(c.Pos(), "empty list in call to or"),
			}
		}
		v := &adt.Vertex{}
		// TODO: make a Disjunction.
		v.AddConjunct(adt.MakeRootConjunct(nil,
			&adt.DisjunctionExpr{Values: d, HasDefaults: false},
		))
		c.Unify(c, v, adt.Finalized)
		return v
	},
}

var divBuiltin = &adt.Builtin{
	Name:   "div",
	Params: []adt.Kind{adt.IntKind, adt.IntKind},
	Result: adt.IntKind,
	Func: func(c *adt.OpContext, args []adt.Value) adt.Expr {
		const name = "argument to div builtin"

		return intDivOp(c, (*big.Int).Div, name, args)
	},
}

var modBuiltin = &adt.Builtin{
	Name:   "mod",
	Params: []adt.Kind{adt.IntKind, adt.IntKind},
	Result: adt.IntKind,
	Func: func(c *adt.OpContext, args []adt.Value) adt.Expr {
		const name = "argument to mod builtin"

		return intDivOp(c, (*big.Int).Mod, name, args)
	},
}

var quoBuiltin = &adt.Builtin{
	Name:   "quo",
	Params: []adt.Kind{adt.IntKind, adt.IntKind},
	Result: adt.IntKind,
	Func: func(c *adt.OpContext, args []adt.Value) adt.Expr {
		const name = "argument to quo builtin"

		return intDivOp(c, (*big.Int).Quo, name, args)
	},
}

var remBuiltin = &adt.Builtin{
	Name:   "rem",
	Params: []adt.Kind{adt.IntKind, adt.IntKind},
	Result: adt.IntKind,
	Func: func(c *adt.OpContext, args []adt.Value) adt.Expr {
		const name = "argument to rem builtin"

		return intDivOp(c, (*big.Int).Rem, name, args)
	},
}

var apdCtx apd.Context

func init() {
	apdCtx = apd.BaseContext
	apdCtx.Precision = 24
}

type intFunc func(z, x, y *big.Int) *big.Int

func intDivOp(c *adt.OpContext, fn intFunc, name string, args []adt.Value) adt.Value {
	a := c.Num(args[0], name)
	b := c.Num(args[1], name)

	if c.HasErr() {
		return nil
	}

	if b.X.IsZero() {
		return c.NewErrf("division by zero")
	}

	var x, y apd.Decimal
	_, _ = apdCtx.RoundToIntegralValue(&x, &a.X)
	if x.Negative {
		x.Coeff.Neg(&x.Coeff)
	}
	_, _ = apdCtx.RoundToIntegralValue(&y, &b.X)
	if y.Negative {
		y.Coeff.Neg(&y.Coeff)
	}

	var d apd.Decimal

	fn(&d.Coeff, &x.Coeff, &y.Coeff)

	if d.Coeff.Sign() < 0 {
		d.Coeff.Neg(&d.Coeff)
		d.Negative = true
	}

	return c.NewNum(&d, adt.IntKind)
}
