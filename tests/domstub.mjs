// sahou build 产物的 Node 测试桩：模拟最小 DOM，验证 页面/page 模块。
"use strict";
var __dom = {};
// --- 最小 DOM 桩 ---
export function makeEl(tag, id) {
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
    parentNode: null,
    setAttribute(k, v) { this.attrs[k] = v; },
    getAttribute(k) { return this.attrs[k]; },
    appendChild(c) { c.parentNode = this; this.children.push(c); },
    removeChild(c) {
      const i = this.children.indexOf(c);
      if (i >= 0) this.children.splice(i, 1);
      c.parentNode = null;
    },
    addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); },
    dispatch(type) { (this.listeners[type] || []).forEach((fn) => fn({ preventDefault() {} })); },
    get parent() { return this.parentNode; }
  };
}
export const document = {
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
export function __saho_test_register(id, el) { __dom[id] = el; }
export const __saho_test_dom = __dom;
