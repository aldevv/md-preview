// latex-render.js: walks the DOM for <div class="latex-pending"> blocks
// emitted by the server, decodes the base64 LaTeX source in
// data-src, hands each one to pandoc.wasm in the browser, then
// replaces the placeholder with the rendered HTML.
//
// Loaded on demand: page.go only wires this script in when the
// rendered body actually has a .latex-pending block to process. The
// pandoc.wasm bundle is ~58 MB raw / ~15 MB gzipped, so pulling it
// in only-when-needed keeps the math-free markdown preview fast.
//
// Sanitizes pandoc output via DOMPurify before injection — pandoc's
// own URL filter handles the common cases but malicious LaTeX could
// still smuggle raw HTML (\begin{html}…). Defense in depth.

import { convert } from "./pandoc.js";

// Keys match pandoc's "default file" YAML schema (the JSON the WASM
// build consumes), NOT the CLI flag names. Notable translations:
//   --mathjax        -> html-math-method: { method: mathjax }
//   --sandbox        -> sandbox: true (same name)
// Reference: https://pandoc.org/MANUAL.html#default-files
//
// We deliberately don't disable Pandoc's syntax-highlighter via
// "highlight-style": that key expects a style name string, not null;
// passing null trips the YAML parser. In practice LaTeX rarely
// contains code blocks pandoc would highlight, and any output styling
// lives inside the .latex-block div where it can't clash with mdp's
// goldmark+highlight.js path.
const PANDOC_OPTS = {
  from: "latex",
  to: "html5",
  "html-math-method": { method: "mathjax" },
  sandbox: true,
};

function decodeSrc(b64) {
  return new TextDecoder().decode(
    Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
  );
}

async function renderOne(node) {
  const src = decodeSrc(node.dataset.src);
  let html;
  try {
    const { stdout, stderr } = await convert(PANDOC_OPTS, src, {});
    if (stderr && stderr.trim()) console.warn("[mdp latex]", stderr);
    html = stdout;
  } catch (e) {
    const msg = String(e && e.message ? e.message : e);
    node.classList.remove("latex-pending");
    node.classList.add("latex-error");
    node.textContent = "LaTeX render error: " + msg;
    return;
  }
  const clean = window.DOMPurify
    ? window.DOMPurify.sanitize(html, { USE_PROFILES: { html: true } })
    : html;
  const wrapper = document.createElement("div");
  wrapper.className = "latex-block";
  if (node.dataset.line) wrapper.dataset.line = node.dataset.line;
  wrapper.innerHTML = clean;
  node.replaceWith(wrapper);
}

async function mdpRenderLatex(root) {
  const nodes = (root || document).querySelectorAll(".latex-pending");
  if (!nodes.length) return;
  for (const node of nodes) {
    await renderOne(node);
  }
  // Re-run KaTeX after pandoc's \(...\) / \[...\] math markers land
  // in the DOM; pandoc's --mathjax mode emits the delimiters KaTeX's
  // auto-render scans for.
  if (typeof window.mdpRenderMath === "function") window.mdpRenderMath();
}

window.mdpRenderLatex = mdpRenderLatex;

// Initial pass. Re-runs are driven by the WS reload handler in page.go,
// which calls window.mdpRenderLatex() after swapping #content.
mdpRenderLatex(document);
