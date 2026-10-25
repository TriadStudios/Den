// A hand-optimized vanilla JS version of the benchmark app: cloned rows,
// one delegated click listener and direct DOM updates.
"use strict";
const A = ["pretty", "large", "big", "small", "tall", "short", "long", "handsome", "plain", "quaint",
  "clean", "elegant", "easy", "angry", "crazy", "helpful", "mushy", "odd", "unsightly", "adorable",
  "important", "inexpensive", "cheap", "expensive", "fancy"];
const C = ["red", "yellow", "blue", "green", "pink", "brown", "purple", "brown", "white", "black", "orange"];
const N = ["table", "chair", "house", "bbq", "desk", "car", "pony", "cookie", "sandwich", "burger",
  "pizza", "mouse", "keyboard"];
let seed = 1;
const rnd = (n) => { seed = (seed * 16807) % 2147483647; return seed % n; };
let nextId = 1;
let data = [];
let selected = null;
const tbody = document.getElementById("tbody");

const tpl = (() => {
  const tr = document.createElement("tr");
  const td1 = document.createElement("td"); td1.className = "col-md-1"; td1.append("");
  const td2 = document.createElement("td"); td2.className = "col-md-4";
  const a1 = document.createElement("a"); a1.className = "lbl"; a1.append(""); td2.append(a1);
  const td3 = document.createElement("td"); td3.className = "col-md-1";
  const a2 = document.createElement("a"); a2.className = "remove";
  const sp = document.createElement("span"); sp.className = "glyphicon glyphicon-remove";
  sp.setAttribute("aria-hidden", "true"); sp.append("×"); a2.append(sp); td3.append(a2);
  const td4 = document.createElement("td"); td4.className = "col-md-6";
  tr.append(td1, td2, td3, td4);
  return tr;
})();

function build(n) {
  const out = new Array(n);
  for (let i = 0; i < n; i++) {
    out[i] = { id: nextId++, label: A[rnd(A.length)] + " " + C[rnd(C.length)] + " " + N[rnd(N.length)], el: null };
  }
  return out;
}

function append(rows) {
  const f = document.createDocumentFragment();
  for (const d of rows) {
    const tr = tpl.cloneNode(true);
    tr.firstChild.firstChild.nodeValue = d.id;
    tr.childNodes[1].firstChild.firstChild.nodeValue = d.label;
    tr.$d = d;
    d.el = tr;
    f.appendChild(tr);
  }
  tbody.appendChild(f);
  for (const d of rows) data.push(d);
}

function clear() {
  data = [];
  selected = null;
  tbody.textContent = "";
}

document.getElementById("run").onclick = () => { clear(); append(build(1000)); };
document.getElementById("runlots").onclick = () => { clear(); append(build(10000)); };
document.getElementById("add").onclick = () => append(build(1000));
document.getElementById("clear").onclick = clear;
document.getElementById("update").onclick = () => {
  for (let i = 0; i < data.length; i += 10) {
    const d = data[i];
    d.label += " !!!";
    d.el.childNodes[1].firstChild.firstChild.nodeValue = d.label;
  }
};
document.getElementById("swaprows").onclick = () => {
  if (data.length <= 998) return;
  const a = data[1], b = data[998];
  data[1] = b; data[998] = a;
  const aNext = a.el.nextSibling;
  tbody.insertBefore(a.el, b.el.nextSibling);
  tbody.insertBefore(b.el, aNext);
};
tbody.onclick = (e) => {
  const a = e.target.closest("a");
  if (!a) return;
  const tr = a.closest("tr");
  if (a.className === "lbl") {
    if (selected) selected.className = "";
    tr.className = "danger";
    selected = tr;
  } else {
    data.splice(data.indexOf(tr.$d), 1);
    if (selected === tr) selected = null;
    tr.remove();
  }
};
