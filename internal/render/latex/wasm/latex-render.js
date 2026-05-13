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

function failAll(msg) {
  document.querySelectorAll(".latex-pending").forEach((node) => {
    node.classList.remove("latex-pending");
    node.classList.add("latex-error");
    node.textContent = "LaTeX init failed: " + msg;
  });
}

let convert;
try {
  ({ convert } = await import("./pandoc.js"));
} catch (e) {
  failAll(String(e && e.message ? e.message : e));
  throw e;
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
  if (typeof window.mdpRenderMath === "function") window.mdpRenderMath();
}

window.mdpRenderLatex = mdpRenderLatex;
mdpRenderLatex(document);
