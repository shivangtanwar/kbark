// SPDX-License-Identifier: Apache-2.0

// Package gitops turns an approved kbark fix into a change to the
// source repository instead of a live change to the cluster: Edit
// rewrites the manifest file in place, and Client opens the pull
// request that carries the change on GitHub.
//
// The two halves are independent: Edit works on bytes, Client works
// over HTTP, and Client.Open is their composition.
package gitops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Action types Edit understands. The strings mirror the incident
// Action* constants; this package stays usable without importing
// incident.
const (
	typeSetMemoryLimit    = "set-memory-limit"
	typeSetEphemeralLimit = "set-ephemeral-storage-limit"
	typeTuneProbe         = "tune-probe"
	typeSetPullPolicy     = "set-image-pull-policy"
	typeFixSelector       = "fix-service-selector"
)

// Sentinel errors returned by Edit. Test with errors.Is.
var (
	// ErrObjectNotFound: no document in the file matches the change's
	// kind/namespace/name, or the container or probe it names is not
	// in the matching document.
	ErrObjectNotFound = errors.New("object not found in manifest")
	// ErrUnsupported: the action type cannot be expressed as an edit
	// of a manifest file; it acts on live cluster state instead
	// (rollback-workload, delete-pod, restart-pod, uncordon-node).
	ErrUnsupported = errors.New("action cannot be made as a file edit")
	// ErrNoChange: the file already carries the target values.
	ErrNoChange = errors.New("manifest already has the target values")
	// ErrTemplated: the file is not plain YAML (it contains Helm
	// template actions), so editing it would not edit what ships.
	ErrTemplated = errors.New("manifest is a template, not plain YAML")
)

// Change is one edit to one object in a manifest file. Type is one of
// the action types listed above and Params are exactly the Params
// kbark stores on the incident's action.
type Change struct {
	// Kind, Namespace and Name identify the object to edit:
	// Deployment, StatefulSet, DaemonSet or Service. A document
	// matches when its kind and metadata.name are equal and its
	// metadata.namespace is equal or absent (Kustomize often sets the
	// namespace outside the file).
	Kind, Namespace, Name string
	// Type is the action; Params mirror incident.Action.Params.
	Type   string
	Params map[string]string
}

// Edit returns file with the change applied to the matching object.
//
// Preservation: every document other than the edited one is copied
// through byte-for-byte. The edited document is re-encoded from its
// parsed node tree with the file's own indentation, which keeps
// comments, key order and scalar quoting, but not everything: yaml.v3
// always indents sequence items one level under their key (so
// "containers:\n  - name: ..." even when the file wrote the dash at
// the key's own column), drops blank lines inside the document and
// puts a blank line before a trailing comment. The output is stable:
// editing the output again reproduces the same formatting.
func Edit(file []byte, c Change) ([]byte, error) {
	if bytes.Contains(file, []byte("{{")) {
		return nil, fmt.Errorf("%w: the file contains template actions", ErrTemplated)
	}
	if !supported(c.Type) {
		return nil, fmt.Errorf("%w: action %q", ErrUnsupported, c.Type)
	}
	docs, err := decodeDocs(file)
	if err != nil {
		return nil, err
	}
	idx := findDoc(docs, c)
	if idx < 0 {
		return nil, fmt.Errorf("%w: no document for %s %q", ErrObjectNotFound, c.Kind, c.Name)
	}
	if err := apply(docs[idx].Content[0], c); err != nil {
		return nil, err
	}
	return encodeDocs(file, docs, idx)
}

// supported reports whether the action can be expressed as an edit of
// a manifest file.
func supported(typ string) bool {
	switch typ {
	case typeSetMemoryLimit, typeSetEphemeralLimit, typeTuneProbe, typeSetPullPolicy, typeFixSelector:
		return true
	}
	return false
}

// apply mutates one document's root node. It mirrors what
// remediate.mutation does to a typed pod template, so what a reviewer
// approves in a pull request is what kbark would have applied live.
func apply(root *yaml.Node, c Change) error {
	switch c.Type {
	case typeSetMemoryLimit:
		return setQuantity(root, c, "memory")
	case typeSetEphemeralLimit:
		return setQuantity(root, c, "ephemeral-storage")
	case typeTuneProbe:
		return tuneProbe(root, c)
	case typeSetPullPolicy:
		return setPullPolicy(root, c)
	case typeFixSelector:
		return setSelector(root, c)
	}
	return fmt.Errorf("%w: action %q", ErrUnsupported, c.Type)
}

// --- set-memory-limit / set-ephemeral-storage-limit ---

// setQuantity raises one resource limit, and with it a request that
// equalled the old limit, so a Guaranteed pod stays Guaranteed.
func setQuantity(root *yaml.Node, c Change, name string) error {
	ctr, err := container(root, c.Params["container"], c.Params["init"] == "true")
	if err != nil {
		return err
	}
	if _, err := resource.ParseQuantity(c.Params["to"]); err != nil {
		return err
	}
	res := child(ctr, "resources")
	old := "0" // an absent limit reads as zero, as on the typed path
	if res != nil {
		if limits := child(res, "limits"); limits != nil {
			if n := child(limits, name); n != nil && n.Kind == yaml.ScalarNode {
				old = n.Value
			}
		}
	}
	if quantityEqual(old, c.Params["to"]) {
		return fmt.Errorf("%w: %s limit of container %q is already %s", ErrNoChange, name, c.Params["container"], c.Params["to"])
	}
	res, err = ensureMap(ctr, "resources")
	if err != nil {
		return err
	}
	limits, err := ensureMap(res, "limits")
	if err != nil {
		return err
	}
	if err := setChildString(limits, name, c.Params["to"]); err != nil {
		return err
	}
	if reqs := child(res, "requests"); reqs != nil {
		if rn := child(reqs, name); rn != nil && rn.Kind == yaml.ScalarNode && quantityEqual(rn.Value, old) {
			setScalar(rn, c.Params["to"])
		}
	}
	return nil
}

// quantityEqual compares quantities the way the API server would:
// 64Mi equals 64Mi and 0.5Gi equals 512Mi. Values the parser rejects
// fall back to trimmed string equality.
func quantityEqual(a, b string) bool {
	qa, erra := resource.ParseQuantity(a)
	qb, errb := resource.ParseQuantity(b)
	if erra == nil && errb == nil {
		return qa.Cmp(qb) == 0
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// --- tune-probe ---

func tuneProbe(root *yaml.Node, c Change) error {
	// Probes are never tuned on init containers, matching mutation.
	ctr, err := container(root, c.Params["container"], false)
	if err != nil {
		return err
	}
	var key string
	switch c.Params["probe"] {
	case "liveness":
		key = "livenessProbe"
	case "startup":
		key = "startupProbe"
	case "readiness":
		key = "readinessProbe"
	default:
		return fmt.Errorf("unknown probe %q", c.Params["probe"])
	}
	probe := child(ctr, key)
	if probe == nil || probe.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: container %q has no %s probe", ErrObjectNotFound, c.Params["container"], c.Params["probe"])
	}
	delay, err1 := strconv.Atoi(c.Params["initialDelaySeconds"])
	timeout, err2 := strconv.Atoi(c.Params["timeoutSeconds"])
	if err1 != nil || err2 != nil {
		return errors.New("invalid probe timing")
	}
	dn, tn := child(probe, "initialDelaySeconds"), child(probe, "timeoutSeconds")
	if v, ok := intVal(dn); ok && v == delay {
		if v, ok := intVal(tn); ok && v == timeout {
			return fmt.Errorf("%w: %s probe of container %q already waits %ds and allows %ds",
				ErrNoChange, c.Params["probe"], c.Params["container"], delay, timeout)
		}
	}
	if err := setChildInt(probe, "initialDelaySeconds", delay); err != nil {
		return err
	}
	return setChildInt(probe, "timeoutSeconds", timeout)
}

// --- set-image-pull-policy ---

func setPullPolicy(root *yaml.Node, c Change) error {
	ctr, err := container(root, c.Params["container"], c.Params["init"] == "true")
	if err != nil {
		return err
	}
	if c.Params["to"] != "IfNotPresent" {
		return fmt.Errorf("unsupported pull policy %q", c.Params["to"])
	}
	if cur := child(ctr, "imagePullPolicy"); cur != nil && cur.Kind == yaml.ScalarNode && cur.Value == c.Params["to"] {
		return fmt.Errorf("%w: pull policy of container %q is already %s", ErrNoChange, c.Params["container"], c.Params["to"])
	}
	return setChildString(ctr, "imagePullPolicy", c.Params["to"])
}

// --- fix-service-selector ---

// setSelector replaces a Service's selector after checking it is
// still the one the fix was planned from.
func setSelector(root *yaml.Node, c Change) error {
	from, err := selectorParam(c, "from")
	if err != nil {
		return err
	}
	to, err := selectorParam(c, "to")
	if err != nil {
		return err
	}
	sel := walk(root, "spec", "selector")
	cur := mapFromNode(sel)
	if maps.Equal(cur, to) {
		return fmt.Errorf("%w: selector of Service %q is already %v", ErrNoChange, c.Name, to)
	}
	if !maps.Equal(cur, from) {
		return fmt.Errorf("selector of Service %q in the file is %v, not the %v the fix was planned from", c.Name, cur, from)
	}
	next := selectorNode(to)
	next.HeadComment, next.LineComment, next.FootComment = sel.HeadComment, sel.LineComment, sel.FootComment
	*sel = *next
	return nil
}

func selectorParam(c Change, key string) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(c.Params[key]), &m); err != nil || len(m) == 0 {
		return nil, fmt.Errorf("stored selector %q is not usable", c.Params[key])
	}
	return m, nil
}

// selectorNode builds a selector mapping with keys sorted, so the
// same change always produces the same diff.
func selectorNode(m map[string]string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		n.Content = append(n.Content, keyNode(k), stringNode(m[k]))
	}
	return n
}

// --- node helpers ---

// container finds a container in the workload's pod template,
// mirroring remediate.findContainer.
func container(root *yaml.Node, name string, init bool) (*yaml.Node, error) {
	spec := walk(root, "spec", "template", "spec")
	if spec == nil || spec.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: no pod template", ErrObjectNotFound)
	}
	list := child(spec, "containers")
	if init {
		list = child(spec, "initContainers")
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%w: container %q not found", ErrObjectNotFound, name)
	}
	for _, ctr := range list.Content {
		if n := child(ctr, "name"); n != nil && n.Kind == yaml.ScalarNode && n.Value == name {
			return ctr, nil
		}
	}
	return nil, fmt.Errorf("%w: container %q not found", ErrObjectNotFound, name)
}

// child returns the value node of key in a mapping, or nil.
func child(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// walk descends a chain of mapping keys; nil when any is missing.
func walk(m *yaml.Node, keys ...string) *yaml.Node {
	cur := m
	for _, k := range keys {
		cur = child(cur, k)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// mapFromNode reads a mapping of string scalars; nil or anything else
// yields an empty map (a missing or null selector).
func mapFromNode(n *yaml.Node) map[string]string {
	m := map[string]string{}
	if n == nil || n.Kind != yaml.MappingNode {
		return m
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind == yaml.ScalarNode && v.Kind == yaml.ScalarNode {
			m[k.Value] = v.Value
		}
	}
	return m
}

// ensureMap returns the mapping under key, creating it when absent,
// replacing a null value with an empty mapping, and switching an
// empty flow mapping (resources: {}) to block style when it gains
// content.
func ensureMap(parent *yaml.Node, key string) (*yaml.Node, error) {
	v := child(parent, key)
	if v == nil {
		v = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		parent.Content = append(parent.Content, keyNode(key), v)
		return v, nil
	}
	if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
		*v = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		return v, nil
	}
	if v.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%q is not a mapping", key)
	}
	if len(v.Content) == 0 && v.Style&yaml.FlowStyle != 0 {
		v.Style = 0
	}
	return v, nil
}

func setChildString(parent *yaml.Node, key, val string) error {
	if n := child(parent, key); n != nil {
		if n.Kind != yaml.ScalarNode {
			return fmt.Errorf("%q is not a scalar", key)
		}
		setScalar(n, val)
		return nil
	}
	parent.Content = append(parent.Content, keyNode(key), stringNode(val))
	return nil
}

func setChildInt(parent *yaml.Node, key string, val int) error {
	if n := child(parent, key); n != nil {
		if n.Kind != yaml.ScalarNode {
			return fmt.Errorf("%q is not a scalar", key)
		}
		n.Tag, n.Style, n.Value = "!!int", 0, strconv.Itoa(val)
		return nil
	}
	parent.Content = append(parent.Content, keyNode(key),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(val)})
	return nil
}

func intVal(n *yaml.Node) (int, bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(n.Value))
	if err != nil {
		return 0, false
	}
	return v, true
}

func keyNode(k string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
}

func stringNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// setScalar overwrites a scalar, keeping the node's quoting when it
// was quoted. Plain scalars are re-quoted by the emitter whenever the
// new value would change meaning (2, true, null, ...).
func setScalar(n *yaml.Node, v string) {
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) == 0 {
		n.Style = 0
	}
	n.Tag = "!!str"
	n.Value = v
}

// --- document scanning and re-encoding ---

func decodeDocs(file []byte) ([]*yaml.Node, error) {
	var docs []*yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(file))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		docs = append(docs, &doc)
	}
	return docs, nil
}

// findDoc locates the document for the change: same kind and
// metadata.name, and a metadata.namespace that matches or is absent.
func findDoc(docs []*yaml.Node, c Change) int {
	for i, doc := range docs {
		root := docRoot(doc)
		if root == nil || root.Kind != yaml.MappingNode {
			continue
		}
		if k := child(root, "kind"); k == nil || k.Kind != yaml.ScalarNode || k.Value != c.Kind {
			continue
		}
		if n := walk(root, "metadata", "name"); n == nil || n.Kind != yaml.ScalarNode || n.Value != c.Name {
			continue
		}
		if ns := walk(root, "metadata", "namespace"); ns != nil && ns.Kind == yaml.ScalarNode &&
			ns.Value != "" && ns.Value != c.Namespace {
			continue
		}
		return i
	}
	return -1
}

func docRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return nil
}

// encodeDocs writes the documents back, splicing: every document but
// the edited one is copied from the raw file byte-for-byte, and only
// the edited document (between its --- separator and the next one, or
// the end of the file) is re-encoded.
func encodeDocs(file []byte, docs []*yaml.Node, edited int) ([]byte, error) {
	offs := lineOffsets(file)
	sep, hasSep := separatorAt(file, offs, docs[edited].Line)
	if edited > 0 && !hasSep {
		// A document after the first always begins with a ---
		// separator; if the parser says otherwise, do not splice.
		return encodeAll(file, docs)
	}
	var out bytes.Buffer
	if edited > 0 {
		out.Write(file[:lineOff(offs, docs[edited].Line)])
	}
	if hasSep {
		writeSeparator(&out, sep)
	}
	enc, err := encodeDoc(docs[edited], detectIndent(docs))
	if err != nil {
		return nil, err
	}
	out.Write(enc)
	tail := len(file)
	if edited+1 < len(docs) {
		tail = lineOff(offs, docs[edited+1].Line)
	}
	out.Write(file[tail:])
	return out.Bytes(), nil
}

// encodeAll re-encodes every document in one stream, used when the
// byte layout of the original cannot be spliced safely.
func encodeAll(file []byte, docs []*yaml.Node) ([]byte, error) {
	var out bytes.Buffer
	if sep, ok := separatorAt(file, lineOffsets(file), docs[0].Line); ok {
		writeSeparator(&out, sep)
	}
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(detectIndent(docs))
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return nil, fmt.Errorf("re-encode manifest: %w", err)
		}
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("re-encode manifest: %w", err)
	}
	return out.Bytes(), nil
}

func encodeDoc(doc *yaml.Node, indent int) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("re-encode manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("re-encode manifest: %w", err)
	}
	return buf.Bytes(), nil
}

// lineOffsets[i] is the byte offset of line i+1.
func lineOffsets(file []byte) []int {
	offs := []int{0}
	for i, b := range file {
		if b == '\n' {
			offs = append(offs, i+1)
		}
	}
	return offs
}

func lineOff(offs []int, line int) int {
	if line < 1 {
		return 0
	}
	if line > len(offs) {
		return offs[len(offs)-1]
	}
	return offs[line-1]
}

// separatorAt reports whether the 1-based line holds the document
// separator, and returns that raw line.
func separatorAt(file []byte, offs []int, line int) (string, bool) {
	if line < 1 || line > len(offs) {
		return "", false
	}
	start := offs[line-1]
	end := len(file)
	if line < len(offs) {
		end = offs[line]
	}
	s := strings.TrimSpace(string(file[start:end]))
	if s == "---" || strings.HasPrefix(s, "--- ") {
		return string(file[start:end]), true
	}
	return "", false
}

func writeSeparator(out *bytes.Buffer, sep string) {
	out.WriteString(sep)
	if !strings.HasSuffix(sep, "\n") {
		out.WriteByte('\n')
	}
}

// detectIndent returns the file's block indentation: the most common
// positive offset between a key and the first key nested under it,
// clamped to the range yaml.v3 accepts. Flow mappings are skipped,
// and so are sequences written with the dash at the key's own column
// (they carry no indentation information). 2 when nothing nests.
// Files are usually consistent; the mode rather than the minimum
// tolerates one author-sloppy mapping without collapsing the rest.
func detectIndent(docs []*yaml.Node) int {
	counts := map[int]int{}
	for _, doc := range docs {
		if root := docRoot(doc); root != nil {
			walkIndent(root, 0, counts)
		}
	}
	best, bestN := 2, 0
	for ind, n := range counts {
		if n > bestN && ind >= 2 && ind <= 9 {
			best, bestN = ind, n
		}
	}
	return best
}

func walkIndent(val *yaml.Node, keyCol int, counts map[int]int) {
	if keyCol > 0 && val.Kind == yaml.MappingNode && val.Style&yaml.FlowStyle == 0 && len(val.Content) > 0 {
		counts[val.Content[0].Column-keyCol]++
	}
	switch val.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(val.Content); i += 2 {
			walkIndent(val.Content[i+1], val.Content[i].Column, counts)
		}
	case yaml.SequenceNode:
		// A mapping item's keys sit past the dash, whose column the
		// node tree does not record, so items are not sampled
		// directly; their nested mappings are.
		for _, item := range val.Content {
			walkIndent(item, 0, counts)
		}
	}
}
