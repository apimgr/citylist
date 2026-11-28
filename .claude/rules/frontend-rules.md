# Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never move application behavior to client-only rendering or require JavaScript for core features.
- Never emit unsafe HTML, inaccessible controls, unbounded page content, or placeholder pages.
- Never let page templates, navigation, styles, or frontend API descriptions contradict the server.

## CRITICAL - ALWAYS DO

- Render pages on the server with the specified templates and progressive enhancement.
- Use responsive, accessible layouts, semantic HTML, keyboard support, and localized text.
- Ensure every server page has complete, current content and behaves without JavaScript.
- Verify pages at supported viewport sizes and keep styles within the project's CSS architecture.

## Key rules

- PART 16 governs every server-rendered page, template, asset, and browser interaction.
- Keep frontend behavior consistent with the API contract, IDEA.md, and current docs.

For complete details, see AI.md PART 16.
