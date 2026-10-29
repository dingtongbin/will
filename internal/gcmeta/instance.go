package gcmeta

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// maxGenericDepth bounds one instantiation chain. A generic class whose field
// mentions a larger instance of itself never terminates, so the expansion is
// reported instead of growing without a bound.
const maxGenericDepth = 64

// builtinGenericArity is the set of predeclared generic types. Their names stay
// unqualified and their instances are not declaration templates.
var builtinGenericArity = map[string]int{
	"Array": 1, "Map": 2, "Set": 1, "Option": 1, "Result": 2,
	"list": 1, "dict": 2, "set": 1, "chan": 1, "Task": 1,
	"Weak": 1, "Pinned": 1, "Handle": 1,
	"rc": 1, "arc": 1, "weak_rc": 1, "weak_arc": 1,
}

// builtinValueNames are predeclared value types that never take a package
// prefix.
var builtinValueNames = map[string]bool{
	"bool": true, "i8": true, "u8": true, "i16": true, "u16": true,
	"i32": true, "u32": true, "f32": true, "i64": true, "u64": true,
	"f64": true, "usize": true, "isize": true, "raw": true,
	"str": true, "bytes": true, "Error": true, "TaskError": true,
	"Value": true, "Show": true, "interface {}": true, "()": true,
}

// canonicalText parses one type text and rewrites every named type to its
// qualified declaration name. A user generic type becomes the canonical
// instance name Name<Arg,...>, and the instance layout is created on demand.
func (p *Program) canonicalText(pkg, text string, depth int) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("empty type text")
	}
	node, err := parseTypeText(text)
	if err != nil {
		return "", err
	}
	return p.canonicalNode(pkg, text, node, depth)
}

func (p *Program) canonicalNode(pkg, text string, node *typeNode, depth int) (string, error) {
	switch node.kind {
	case typeUnit:
		return "()", nil
	case typeView:
		inner, err := p.canonicalNode(pkg, typeTextOf(node.elem), node.elem, depth)
		if err != nil {
			return "", err
		}
		return "[]" + inner, nil
	case typeArray:
		inner, err := p.canonicalNode(pkg, typeTextOf(node.elem), node.elem, depth)
		if err != nil {
			return "", err
		}
		return "[" + fmt.Sprint(node.length) + "]" + inner, nil
	case typeFunc:
		return node.name, nil
	}
	name := node.name
	if strings.HasPrefix(text, "ref ") || strings.HasPrefix(text, "mut ") {
		prefix := text[:4]
		inner, err := p.canonicalText(pkg, text[4:], depth)
		if err != nil {
			return "", err
		}
		return prefix + inner, nil
	}
	if arity, ok := builtinGenericArity[name]; ok {
		if len(node.args) != arity {
			return "", fmt.Errorf("type %s needs %d type arguments", name, arity)
		}
		args, err := p.canonicalArgs(pkg, node.args, depth)
		if err != nil {
			return "", err
		}
		return name + "<" + strings.Join(args, ",") + ">", nil
	}
	if builtinValueNames[name] {
		return name, nil
	}
	name = p.aliasName(pkg, name)
	if tmpl, ok := p.resolveTemplate(pkg, name); ok {
		if len(node.args) == 0 {
			return "", fmt.Errorf("generic type %s needs type arguments", name)
		}
		if len(node.args) != len(tmpl.params) {
			return "", fmt.Errorf("type %s takes %d type arguments, got %d",
				name, len(tmpl.params), len(node.args))
		}
		args, err := p.canonicalArgs(pkg, node.args, depth)
		if err != nil {
			return "", err
		}
		for _, arg := range args {
			if p.mentionsTypeParam(arg) {
				// The use is inside the template itself, not a concrete
				// instantiation, so it has no runtime layout yet.
				return text, nil
			}
		}
		return p.ensureInstance(tmpl, args, depth)
	}
	if decl, ok := p.resolveDeclExact(pkg, name); ok {
		if len(node.args) != 0 {
			return "", fmt.Errorf("type %s takes no type arguments", name)
		}
		return decl.name, nil
	}
	// An unknown name stays as written so layout reports the missing
	// declaration at the point that actually needs it.
	return name, nil
}

func (p *Program) canonicalArgs(pkg string, args []*typeNode, depth int) ([]string, error) {
	out := make([]string, len(args))
	for i, arg := range args {
		text, err := p.canonicalNode(pkg, typeTextOf(arg), arg, depth)
		if err != nil {
			return nil, err
		}
		out[i] = text
	}
	return out, nil
}

// resolveTemplate finds one generic declaration by qualified name. A short
// name resolves in the current package first and then by unique suffix.
func (p *Program) resolveTemplate(pkg, name string) (*declaration, bool) {
	if d, ok := p.templates[name]; ok {
		return d, true
	}
	if q := qualify(pkg, name); q != "" {
		if d, ok := p.templates[q]; ok {
			return d, true
		}
	}
	var found *declaration
	for q, d := range p.templates {
		if strings.HasSuffix(q, "."+name) {
			if found != nil {
				return nil, false
			}
			found = d
		}
	}
	return found, found != nil
}

// resolveDeclExact finds one concrete declaration by qualified name, including
// a monomorphized instance.
func (p *Program) resolveDeclExact(pkg, name string) (*declaration, bool) {
	if d, ok := p.decls[name]; ok {
		return d, true
	}
	if q := qualify(pkg, name); q != "" {
		if d, ok := p.decls[q]; ok {
			return d, true
		}
	}
	var found *declaration
	for q, d := range p.decls {
		if strings.HasSuffix(q, "."+name) {
			if found != nil {
				return nil, false
			}
			found = d
		}
	}
	return found, found != nil
}

// ensureInstance creates the declaration of one generic instance. The
// declaration is registered before its fields are canonicalized, so a field
// that mentions the same instance stops at the table entry instead of
// recursing.
func (p *Program) ensureInstance(tmpl *declaration, args []string, depth int) (string, error) {
	key := tmpl.name + "<" + strings.Join(args, ",") + ">"
	if _, ok := p.decls[key]; ok {
		return key, nil
	}
	if p.sealed {
		return "", fmt.Errorf("generic instance %s was not discovered during planning", key)
	}
	if depth >= maxGenericDepth {
		return "", fmt.Errorf("generic instantiation of %s does not terminate", tmpl.name)
	}
	inst := &declaration{name: key, kind: tmpl.kind, pkg: tmpl.pkg}
	p.decls[key] = inst
	subst := make(map[string]string, len(tmpl.params))
	for i, param := range tmpl.params {
		subst[param] = args[i]
	}
	for _, f := range tmpl.fields {
		copied := cloneRecord(f)
		for i := range copied.Fields {
			field := &copied.Fields[i]
			if field.Name != "type" || field.Value.Kind != willc.TextValue {
				continue
			}
			text := substituteTypeParams(field.Value.Text, subst)
			canon, err := p.canonicalText(tmpl.pkg, text, depth+1)
			if err != nil {
				delete(p.decls, key)
				return "", fmt.Errorf("%s field %s: %w", key, fieldText(copied, "text"), err)
			}
			field.Value = willc.Text(canon)
		}
		inst.fields = append(inst.fields, copied)
	}
	for _, v := range tmpl.variants {
		copied, err := p.instanceVariant(tmpl.pkg, v, subst, depth+1)
		if err != nil {
			delete(p.decls, key)
			return "", fmt.Errorf("%s variant %s: %w", key, fieldText(v, "text"), err)
		}
		inst.variants = append(inst.variants, copied)
	}
	return key, nil
}

// instanceVariant clones one enum variant of a generic instance and copies its
// argument records so their type texts can be substituted. The copies get new
// ids because the originals belong to the template and are shared.
func (p *Program) instanceVariant(pkg string, v willc.Record, subst map[string]string, depth int) (willc.Record, error) {
	copied := cloneRecord(v)
	copied.ID = p.nextRecordID
	p.nextRecordID++
	for i := range copied.Fields {
		field := &copied.Fields[i]
		if field.Name != "arg" || field.Value.Kind != willc.RefValue {
			continue
		}
		arg, ok := p.records[field.Value.Ref]
		if !ok {
			return willc.Record{}, fmt.Errorf("variant argument #%d is missing", field.Value.Ref)
		}
		argCopy := cloneRecord(arg)
		argCopy.ID = p.nextRecordID
		p.nextRecordID++
		for j := range argCopy.Fields {
			af := &argCopy.Fields[j]
			if af.Name != "text" || af.Value.Kind != willc.TextValue {
				continue
			}
			text := substituteTypeParams(af.Value.Text, subst)
			canon, err := p.canonicalText(pkg, text, depth)
			if err != nil {
				return willc.Record{}, err
			}
			af.Value = willc.Text(canon)
		}
		p.records[argCopy.ID] = argCopy
		field.Value = willc.Ref(argCopy.ID)
	}
	p.records[copied.ID] = copied
	return copied, nil
}

func cloneRecord(r willc.Record) willc.Record {
	out := willc.Record{ID: r.ID, Kind: r.Kind}
	out.Fields = append(out.Fields, r.Fields...)
	return out
}

// substituteTypeParams replaces whole type-parameter names inside one type
// text. Identifiers are matched as a whole so a parameter T never rewrites
// Task.
func substituteTypeParams(text string, subst map[string]string) string {
	if len(subst) == 0 {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if !isTypeIdentByte(text[i]) {
			b.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) && isTypeIdentByte(text[j]) {
			j++
		}
		word := text[i:j]
		if repl, ok := subst[word]; ok {
			b.WriteString(repl)
		} else {
			b.WriteString(word)
		}
		i = j
	}
	return b.String()
}

func isTypeIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// mentionsTypeParam reports whether one type text names a declared type
// parameter anywhere in its shape.
func (p *Program) mentionsTypeParam(text string) bool {
	for i := 0; i < len(text); {
		if !isTypeIdentByte(text[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(text) && isTypeIdentByte(text[j]) {
			j++
		}
		if p.typeParams[text[i:j]] {
			return true
		}
		i = j
	}
	return false
}

// addImport records one package alias for type-text canonicalization. The
// default alias of a path is its last segment.
func (p *Program) addImport(pkg, path, alias string) {
	if pkg == "" || path == "" {
		return
	}
	if alias == "" {
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			alias = path[i+1:]
		} else {
			alias = path
		}
	}
	aliases := p.imports[pkg]
	if aliases == nil {
		aliases = map[string]string{}
		p.imports[pkg] = aliases
	}
	aliases[alias] = path
}

// aliasName rewrites one type name written as alias.Name into the imported
// package path. A name without a matching import alias stays unchanged.
func (p *Program) aliasName(pkg, name string) string {
	dot := strings.IndexByte(name, '.')
	if dot <= 0 {
		return name
	}
	aliases := p.imports[pkg]
	if aliases == nil {
		return name
	}
	path, ok := aliases[name[:dot]]
	if !ok {
		return name
	}
	return path + name[dot:]
}
