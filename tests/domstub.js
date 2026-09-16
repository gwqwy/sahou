// sahou build 产物的 Node 测试桩：模拟最小 DOM，验证 页面/page 模块。
"use strict";
// --- 最小 DOM 桩 ---
function makeEl(tag, id) {
  return {
    __saho_element: true,
    tagName: tag,
    id: id || "",
    textContent: "",
    value: "",
    style: {},
    attrs: {},
    children: [],
    listeners: {},
    parent: null,
    setAttribute(k, v) { this.attrs[k] = v; },
    getAttribute(k) { return this.attrs[k]; },
    appendChild(c) { c.parent = this; this.children.push(c); },
    removeChild(c) {
      const i = this.children.indexOf(c);
      if (i >= 0) this.children.splice(i, 1);
      c.parent = null;
    },
    addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); },
    dispatch(type) { (this.listeners[type] || []).forEach((fn) => fn({ preventDefault() {} })); }
  };
}
const __dom = {};
globalThis.document = {
  querySelector(sel) {
    const m = sel.match(/^#(.+)$/);
    if (m && __dom[m[1]]) return __dom[m[1]];
    return null;
  },
  querySelectorAll(sel) {
    const m = sel.match(/^#(.+)$/);
    return m && __dom[m[1]] ? [__dom[m[1]]] : [];
  },
  createElement(tag) { return makeEl(tag); }
};
globalThis.__saho_test_register = function (id, el) { __dom[id] = el; };
globalThis.__saho_test_dom = __dom;
