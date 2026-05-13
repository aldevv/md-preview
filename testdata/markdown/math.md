# Markdown with KaTeX math

mdp bundles KaTeX (conditionally — only when math markers are present
in the rendered body) so display + inline math render client-side.

## Display math

Block math via double-dollar delimiters:

$$
\int_{-\infty}^{\infty} e^{-x^2}\,dx = \sqrt{\pi}
$$

Block math via `\[...\]`:

\[
  e^{i\pi} + 1 = 0
\]

## Inline math

The Pythagorean theorem states that \(a^2 + b^2 = c^2\) for any
right triangle. Greek letters: alpha \(\alpha\), beta \(\beta\),
gamma \(\gamma\), delta \(\delta\), pi \(\pi\), omega \(\omega\).

## Mixed with prose

Reading the formula $$E = mc^2$$ aloud is one of physics's most
recognizable rituals.
