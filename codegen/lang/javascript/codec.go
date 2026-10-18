package javascript

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// CodecHelper is the Helpers flag a call of json.decode, json.encode,
// http.send or http.get sets, and CodecHelperJS the runtime the page then
// declares: it reads and writes the program's own values through a
// descriptor of their type, which a JavaScript value cannot give.
const CodecHelper = "sngl:codec"

func init() {
	codegen.DeclareLangImplements(langJS, ir.JSONDecodeIntrinsic, ir.JSONEncodeIntrinsic, ir.HTTPSendIntrinsic, ir.HTTPGetIntrinsic)
	codegen.RegisterIntrinsic(langJS, "http.pathEscape", func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return jsEscape(tr(args[0])), nil
	})
}

// jsEscape percent-encodes every byte but the unreserved characters, which is
// encodeURIComponent with the five marks it leaves alone encoded too.
func jsEscape(s string) string {
	return "encodeURIComponent(" + s + `).replace(/[!'()*]/g, (c) => "%" + c.charCodeAt(0).toString(16).toUpperCase())`
}

// codecCall renders a call of a codec intrinsic over CodecHelperJS.
func (jc *JsIRContext) codecCall(n *ir.Call) (string, bool) {
	if !codegen.IsCodecCall(n) {
		return "", false
	}
	if jc.Ctx != nil && jc.Ctx.Helpers != nil {
		jc.Ctx.Helpers[CodecHelper] = true
	}
	t, format, _ := ir.CodecCall(n)
	arg := func(name string) string {
		if a := n.ArgFor(name); a != nil {
			return jc.EvalExpr(a)
		}
		return "null"
	}
	str := ir.TypString
	switch n.Func.Intrinsic {
	case ir.JSONEncodeIntrinsic:
		return fmt.Sprintf("_snglJSONEncode(%s, %s)", arg("value"), jsCodec(ir.PlanCodec(t, format))), true
	case ir.JSONDecodeIntrinsic:
		failed := jc.EvalExpr(codegen.ResultFailed(n.Type, "decode", codegen.HostValue("__r.e", str)))
		ok := jc.EvalExpr(codegen.ResultOK(n.Type, codegen.HostValue("__r.v", t)))
		return fmt.Sprintf("((__r) => __r.e !== undefined ? %s : %s)(_snglJSONDecode(%s, %s))", failed, ok, arg("text"), jsCodec(ir.PlanCodec(t, format))), true
	}
	method := `"get"`
	if n.Func.Intrinsic == ir.HTTPSendIntrinsic {
		method = arg("method")
	}
	url := arg("url")
	if t != nil && len(ir.PlanCodec(t, format).Root.Fields) > 0 {
		url = fmt.Sprintf("_snglWithQuery(%s, %s, %s)", url, arg("query"), jsCodec(ir.PlanCodec(t, format)))
	}
	headers, body := arg("headers"), arg("body")
	st, ht, bt := codegen.ResponseFieldTypes(n.Type)
	msg := codegen.HostValue("__r.m", str)
	resp := codegen.CodecResponse(n.Type, codegen.HostValue("__r.s", st), codegen.HostValue("__r.h", ht), codegen.HostValue("__r.b", bt))
	call := fmt.Sprintf("((__r) => __r.f ? (__r.t ? %s : %s) : %s)(await _snglHTTPSend(%s, %s, %s, %s))",
		jc.EvalExpr(codegen.ResultFailed(n.Type, "timeout", msg)), jc.EvalExpr(codegen.ResultFailed(n.Type, "transport", msg)),
		jc.EvalExpr(codegen.ResultOK(n.Type, resp)), method, url, headers, body)
	if n.Detached {
		// Started rather than awaited: the request runs, and nothing waits
		// for what it answers.
		return "(async () => " + call + ")()", true
	}
	return call, true
}

// jsCodec spells a codec plan as the descriptor CodecHelperJS reads: an
// object, or a function declaring each struct and enum first where the plan
// names any, so a type that holds itself points back at its own.
func jsCodec(p *ir.CodecPlan) string {
	if len(p.Named) == 0 {
		return jsCodecNode(p.Root)
	}
	var b strings.Builder
	b.WriteString("(() => { ")
	for _, n := range p.Named {
		if n.Kind == ir.CodecEnum {
			wires := make([]string, len(n.Members))
			names := make([]string, len(n.Members))
			for i, m := range n.Members {
				wires[i] = strconv.Quote(m.WireName())
				names[i] = strconv.Quote(m.Name)
			}
			fmt.Fprintf(&b, "const c%d = {k: \"e\", w: [%s], n: [%s]}; ", n.ID, strings.Join(wires, ", "), strings.Join(names, ", "))
			continue
		}
		fmt.Fprintf(&b, "const c%d = {k: \"s\"}; ", n.ID)
	}
	for _, n := range p.Named {
		if n.Kind != ir.CodecStruct {
			continue
		}
		sd, _ := n.Type.Decl.(*ir.StructDef)
		fields := make([]string, len(n.Fields))
		for i, f := range n.Fields {
			fields[i] = fmt.Sprintf("[%s, %s, %s]", strconv.Quote(jsFieldKey(sd, f.Name)), strconv.Quote(f.Wire), jsCodecNode(f.Codec))
		}
		fmt.Fprintf(&b, "c%d.f = [%s]; ", n.ID, strings.Join(fields, ", "))
	}
	fmt.Fprintf(&b, "return %s; })()", jsCodecNode(p.Root))
	return b.String()
}

func jsCodecNode(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecStruct, ir.CodecEnum:
		return "c" + strconv.Itoa(c.ID)
	case ir.CodecOption, ir.CodecList, ir.CodecMap:
		return fmt.Sprintf("{k: %q, e: %s}", jsCodecKind[c.Kind], jsCodecNode(c.Elem))
	}
	return fmt.Sprintf("{k: %q}", jsCodecKind[c.Kind])
}

var jsCodecKind = map[ir.CodecKind]string{
	ir.CodecBool:     "b",
	ir.CodecInt:      "i",
	ir.CodecFloat:    "f",
	ir.CodecString:   "str",
	ir.CodecDate:     "d",
	ir.CodecDatetime: "dt",
	ir.CodecOption:   "o",
	ir.CodecList:     "l",
	ir.CodecMap:      "m",
}

// CodecHelperJS is the runtime behind CodecHelper. Its rules and its words
// are pkg/go/sngljson's: a key the type does not name is ignored, a missing
// key fails unless its field is an option, a value of the wrong kind fails
// naming where it was, writing leaves an absent option's key out, and a map's
// keys are visited in order so a failure names the same one everywhere.
const CodecHelperJS = `function _snglCodecFail(m) { const e = new Error(m); e._snglCodec = true; return e; }
function _snglDescribe(c) {
  switch (c.k) {
    case "b": return "a boolean";
    case "i": return "an integer";
    case "f": return "a number";
    case "str": return "a string";
    case "d": return "a date";
    case "dt": return "a datetime";
    case "o": return _snglDescribe(c.e);
    case "l": return "an array";
    case "e": return "one of " + c.w.join(", ");
  }
  return "an object";
}
const _snglDateRE = /^\d{4}-\d{2}-\d{2}$/;
const _snglDatetimeRE = /^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;
function _snglValidDate(s) {
  const y = +s.slice(0, 4), m = +s.slice(5, 7), d = +s.slice(8, 10);
  const t = new Date(0);
  t.setUTCFullYear(y, m - 1, d);
  return t.getUTCFullYear() === y && t.getUTCMonth() === m - 1 && t.getUTCDate() === d;
}
function _snglValidDatetime(s) {
  const h = +s.slice(11, 13), mi = +s.slice(14, 16), se = +s.slice(17, 19);
  return _snglValidDate(s) && h < 24 && mi < 60 && se < 60;
}
function _snglIsObject(v) { return v !== null && typeof v === "object" && !Array.isArray(v); }
function _snglDec(v, c, path) {
  const want = () => _snglCodecFail(path + ": want " + _snglDescribe(c));
  switch (c.k) {
    case "o": return v === null ? null : _snglDec(v, c.e, path);
    case "b": if (typeof v !== "boolean") throw want(); return v;
    case "i": if (typeof v !== "number" || !Number.isInteger(v)) throw want(); return v;
    case "f": if (typeof v !== "number") throw want(); return v;
    case "str": if (typeof v !== "string") throw want(); return v;
    case "d": if (typeof v !== "string" || !_snglDateRE.test(v) || !_snglValidDate(v)) throw want(); return v;
    case "dt": if (typeof v !== "string" || !_snglDatetimeRE.test(v) || !_snglValidDatetime(v)) throw want(); return v;
    case "e": { const i = typeof v === "string" ? c.w.indexOf(v) : -1; if (i < 0) throw want(); return c.n[i]; }
    case "l": if (!Array.isArray(v)) throw want(); return v.map((x, i) => _snglDec(x, c.e, path + "[" + i + "]"));
    case "m": {
      if (!_snglIsObject(v)) throw want();
      const out = new Map();
      for (const k of Object.keys(v).sort()) out.set(k, _snglDec(v[k], c.e, path + "." + k));
      return out;
    }
    case "s": {
      if (!_snglIsObject(v)) throw want();
      const out = {};
      for (const [key, wire, fc] of c.f) {
        const present = Object.prototype.hasOwnProperty.call(v, wire);
        if (!present && fc.k !== "o") throw _snglCodecFail(path + "." + wire + ": missing");
        out[key] = _snglDec(present ? v[wire] : null, fc, path + "." + wire);
      }
      return out;
    }
  }
  throw want();
}
function _snglJSONDecode(text, c) {
  let v;
  try { v = JSON.parse(text); } catch (e) { return {e: "not JSON"}; }
  try { return {v: _snglDec(v, c, "$")}; } catch (e) { if (e && e._snglCodec) return {e: e.message}; throw e; }
}
function _snglEnumWire(c, v) { const i = c.n.indexOf(v); return i < 0 ? v : c.w[i]; }
function _snglEnc(v, c) {
  switch (c.k) {
    case "o": return v === null || v === undefined ? "null" : _snglEnc(v, c.e);
    case "b": return v ? "true" : "false";
    case "i": case "f": return JSON.stringify(v);
    case "str": case "d": case "dt": return JSON.stringify(v);
    case "e": return JSON.stringify(_snglEnumWire(c, v));
    case "l": return "[" + v.map((x) => _snglEnc(x, c.e)).join(",") + "]";
    case "m": return "{" + [...v.keys()].sort().map((k) => JSON.stringify(k) + ":" + _snglEnc(v.get(k), c.e)).join(",") + "}";
    case "s": {
      const parts = [];
      for (const [key, wire, fc] of c.f) {
        const x = v[key];
        if (fc.k === "o" && (x === null || x === undefined)) continue;
        parts.push(JSON.stringify(wire) + ":" + _snglEnc(x, fc));
      }
      return "{" + parts.join(",") + "}";
    }
  }
  return "null";
}
function _snglJSONEncode(v, c) { return _snglEnc(v, c); }
function _snglEscape(s) { return encodeURIComponent(s).replace(/[!'()*]/g, (c) => "%" + c.charCodeAt(0).toString(16).toUpperCase()); }
function _snglQueryValues(v, c) {
  switch (c.k) {
    case "o": return v === null || v === undefined ? [] : _snglQueryValues(v, c.e);
    case "l": return v.flatMap((x) => _snglQueryValues(x, c.e));
    case "b": return [v ? "true" : "false"];
    case "i": case "f": return [JSON.stringify(v)];
    case "e": return [_snglEnumWire(c, v)];
  }
  return [String(v)];
}
function _snglWithQuery(url, q, c) {
  const parts = [];
  for (const [key, wire, fc] of c.f) for (const s of _snglQueryValues(q[key], fc)) parts.push(_snglEscape(wire) + "=" + _snglEscape(s));
  if (parts.length === 0) return url;
  return url + (url.includes("?") ? "&" : "?") + parts.join("&");
}
async function _snglHTTPSend(method, url, headers, body) {
  const init = {method: String(method).toUpperCase(), headers: {}};
  if (headers) for (const [k, v] of headers) init.headers[k] = v;
  if (body !== null && body !== undefined) init.body = body;
  let resp, text;
  try {
    resp = await fetch(url, init);
    text = await resp.text();
  } catch (e) {
    return {f: true, t: !!e && e.name === "TimeoutError", m: String((e && e.message) || e)};
  }
  const h = new Map();
  resp.headers.forEach((v, k) => h.set(k.toLowerCase(), v));
  return {s: resp.status, h: h, b: text};
}
`
