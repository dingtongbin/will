package gen

import "fmt"

// showScalar reports whether a predeclared type shows itself. Every scalar
// does: the value is loaded from the interface data pointer and formatted by
// one runtime helper.
func showScalar(text string) bool {
	switch text {
	case "bool", "str":
		return true
	}
	_, _, ok := llvmScalar(text)
	return ok
}

// showThunkName returns the Show method of one predeclared scalar and queues
// the thunk that implements it. The thunk is defined once per module.
func (g *generator) showThunkName(concrete string) (string, bool) {
	if !showScalar(concrete) {
		return "", false
	}
	sym := "will.show." + concrete
	if !g.showEmitted[sym] {
		g.showEmitted[sym] = true
		g.pendingShows = append(g.pendingShows, concrete)
	}
	g.showUsed = true
	return sym, true
}

// emitShowThunks writes the Show method of every predeclared scalar this
// module passes as Show. Each thunk takes the data pointer of the interface
// value and returns the formatted str.
func (g *generator) emitShowThunks() {
	for _, concrete := range g.pendingShows {
		g.emitShowThunk(concrete)
	}
	g.pendingShows = nil
}

func (g *generator) emitShowThunk(concrete string) {
	sym := llvmSymbol("will.show." + concrete)
	fmt.Fprintf(g.out, "define internal { ptr, i64 } @%s(ptr %%data) {\nentry:\n", sym)
	fmt.Fprintf(g.out, "  %%out = alloca { ptr, i64 }\n")
	call := ""
	switch concrete {
	case "str":
		g.queueCDecl("willrt_show_str", "void", []string{"ptr", "i64", "ptr"})
		fmt.Fprintf(g.out, "  %%v = load { ptr, i64 }, ptr %%data\n")
		fmt.Fprintf(g.out, "  %%d = extractvalue { ptr, i64 } %%v, 0\n")
		fmt.Fprintf(g.out, "  %%n = extractvalue { ptr, i64 } %%v, 1\n")
		call = "  call void @willrt_show_str(ptr %d, i64 %n, ptr %out)\n"
	case "bool":
		g.queueCDecl("willrt_show_bool", "void", []string{"i32", "ptr"})
		fmt.Fprintf(g.out, "  %%v = load i1, ptr %%data\n")
		fmt.Fprintf(g.out, "  %%w = zext i1 %%v to i32\n")
		call = "  call void @willrt_show_bool(i32 %w, ptr %out)\n"
	default:
		typ, signed, ok := llvmScalar(concrete)
		if !ok {
			g.out.WriteString("  unreachable\n}\n")
			return
		}
		if typ == "double" || typ == "float" {
			g.queueCDecl("willrt_show_f64", "void", []string{"double", "ptr"})
			fmt.Fprintf(g.out, "  %%v = load %s, ptr %%data\n", typ)
			if typ == "float" {
				fmt.Fprintf(g.out, "  %%w = fpext float %%v to double\n")
				call = "  call void @willrt_show_f64(double %w, ptr %out)\n"
			} else {
				call = "  call void @willrt_show_f64(double %v, ptr %out)\n"
			}
			break
		}
		fn := "willrt_show_u64"
		op := "zext"
		if signed {
			fn = "willrt_show_i64"
			op = "sext"
		}
		g.queueCDecl(fn, "void", []string{"i64", "ptr"})
		fmt.Fprintf(g.out, "  %%v = load %s, ptr %%data\n", typ)
		if typ == "i64" {
			call = fmt.Sprintf("  call void @%s(i64 %%v, ptr %%out)\n", fn)
		} else {
			fmt.Fprintf(g.out, "  %%w = %s %s %%v to i64\n", op, typ)
			call = fmt.Sprintf("  call void @%s(i64 %%w, ptr %%out)\n", fn)
		}
	}
	g.out.WriteString(call)
	fmt.Fprintf(g.out, "  %%s = load { ptr, i64 }, ptr %%out\n")
	fmt.Fprintf(g.out, "  ret { ptr, i64 } %%s\n}\n")
}
