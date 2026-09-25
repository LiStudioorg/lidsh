# DeepSeek Harness — Design Token & Layout Reference (Vue 3 port)

> Extracted faithfully from `docs/ui-tokens.md`. Hex values are transcribed verbatim from the source — the light/dark columns of the alias table are `getComputedStyle` results (static vars substituted). No colors are invented. Where a value is theme-shared (listed once), it is recorded once.
>
> Vendor: `@deepseek-ai/dsh@0.1.5-rc.3`. Only theme switching is `body[data-ds-dark-theme]` + `color-scheme`. No `[data-theme=…]`, no `.dark`, no `prefers-color-scheme` CSS branch. CSS Modules, `data-*` state attributes (no `--modifier` classes).

---

## 1. Theme mechanism

- **Light/dark single switch**: presence of the `data-ds-dark-theme` **attribute on `<body>`** (`data-ds-dark-theme` present = dark). Source: `dsh-client-ui-layout/lib/client.js:443` (`const DARK_ATTRIBUTE = "data-ds-dark-theme"`), `:464–472` (`apply()` sets `document.documentElement.style.colorScheme = scheme`, and `if (scheme === "dark") body.setAttribute(DARK_ATTRIBUTE,"") else body.removeAttribute(DARK_ATTRIBUTE)`).
- **`color-scheme`** is written **inline on `<html>`** (`style="color-scheme: light|dark"`).
- **Token blocks** are mounted on the selector `body` (light) and `body[data-ds-dark-theme]` (dark), *not* `:root`. Only `--dsw-font-family` / `--ds-font-family-code` / `--ds-ease-in-out` / `--ds-transition-duration*` and the `--shiki-*` baseline live on `:root`. So the theme attribute goes on `<body data-ds-dark-theme>` and token blocks also use `body{}`/`body[data-ds-dark-theme]{}`, otherwise descendant priority breaks. Source: `docs/ui-tokens.md §4.3`; token tables `dsh-client-ui-theme/lib/client.js:1052`.
- **Static palette (`--dsw-static-*`) is essentially theme-invariant**: of 73 static tokens, only **one** differs between light and dark — `--dsw-static-neutral-bluish-60` light `#f5f6f7` / dark `#f9fafb`. All theme switching is done by the **alias layer**. Source `:1052`.
- **Font-size axis**: `<body style="--dsh-content-font-size: 14px">`; range **12–17 px, default 14** (`dsh-client-ui-theme/lib/client.js:906`, `Schema.number().step(1).min(12).max(17).default(14)`). Derived:
  - `--dsh-content-font-delta: calc(var(--dsh-content-font-size,14px) - 14px)` → `0px` at 14.
  - `--dsh-content-font-size-secondary: min(calc(fs - 1px), max(13px, calc(fs - 2px)))` → `13px` at 14.
  - `--dsh-content-font-delta-secondary: calc(var(--dsh-content-font-size-secondary) - 13px)` → `0px` at 14.
  (Source `dsh-client-ui-theme/lib/client.js:1058`.)
- JS migration: `document.body.toggleAttribute('data-ds-dark-theme', isDark)` + `document.documentElement.style.colorScheme = isDark ? 'dark' : 'light'` + `document.body.style.setProperty('--dsh-content-font-size', px+'px')` (12–17, default 14).
- **Corner shape** (optional, browsers that support `corner-shape`): `@supports (corner-shape: superellipse(1.5)){ :root{ --dsw-corner-shape: superellipse(1.5) } }` and `*, *::before, *::after { corner-shape: var(--dsw-corner-shape) }`. Circles/capsules must pair `border-radius:50%`/`999px` **with** `corner-shape:round`. Can be skipped entirely on browsers without `corner-shape` (degrades to normal arcs). Source `:1049`.

### Recommended CSS structure
```css
:root { /* --dsw-font-family, --ds-font-family-code, --ds-ease-in-out, --ds-transition-duration*, --shiki-* baseline */ }
body  { /* --dsw-static-* + --dsw-alias-* + --dsw-specific-* + shadows + font ladder + scrollbar */ }
body[data-ds-dark-theme] { /* only --dsw-alias-* / --dsw-specific-* / 2 gradients / 9 --shiki-token-* */ }
```
Note: the alias table's two computed-value columns can be written flat (dropping the static layer), but keeping the static layer helps semantic alignment. Source §8.1.

---

## 2. Semantic alias tokens (light / dark) — the ~50 a UI needs

All from `design-platform.css` (`dsh-client-ui-theme/lib/client.js:1052`), selector `body` / `body[data-ds-dark-theme]`. Both columns are computed final hex values.

### Background
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-bg-base` | `#fff` | `#151517` |
| `--dsw-alias-bg-layer-1` | `#fff` | `#232324` |
| `--dsw-alias-bg-layer-2` | `#fff` | `#2c2c2e` |
| `--dsw-alias-bg-layer-3` | `#fff` | `#353638` |
| `--dsw-alias-bg-mask-1` | `#0000003d` | `#00000080` |
| `--dsw-alias-bg-mask-2` | `#0000001f` | `#0003` |
| `--dsw-alias-bg-mask-3` | `#0000007a` | `#0000007a` (shared) |
| `--dsw-alias-bg-mask-drop` | `#ffffffb3` | `#272730b3` |
| `--dsw-alias-bg-mask-photo` | `#000000e0` | `#000000e0` (shared) |
| `--dsw-alias-bg-module-platform` | `#f5f6f7` | `#353638` |
| `--dsw-alias-bg-multi-select` | `#f5f6f7` | `#212123` |
| `--dsw-alias-bg-overlay` | `#e9ecf2` | `#61666b` |
| `--dsw-alias-bg-skeleton` | `#0000000a` | `#ffffff14` |

### Border
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-border-inverted` | `#0000` | `#ffffff0f` |
| `--dsw-alias-border-inverted2` | `#0000` | `#ffffff14` |
| `--dsw-alias-border-l1` | `#0000000a` | `#ffffff0f` |
| `--dsw-alias-border-l2` | `#0000001a` | `#ffffff1f` |
| `--dsw-alias-border-l2-darkmode-thin` | `#0000001a` | `#ffffff0f` |
| `--dsw-alias-border-l3` | `#0000001f` | `#ffffff29` |
| `--dsw-alias-border-l4` | `#00000029` | `#fff3` |

### Text (label) / link
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-label-caption` | `#adb2b8` | `#81858c` |
| `--dsw-alias-label-dimmed` | `#e1e5ee` | `#43454a` |
| `--dsw-alias-label-primary` | `#0f1115` | `#f9fafb` |
| `--dsw-alias-label-primary-bluish` | `#0e3074` | `#f9fafb` |
| `--dsw-alias-label-primary-dimmed` | `#151517` | `#ebeef2` |
| `--dsw-alias-label-primary-foreground` | `#fff` | `#0f1115` |
| `--dsw-alias-label-primary-inverted` | `#fff` | `#353638` |
| `--dsw-alias-label-secondary` | `#61666b` | `#cfd3d6` |
| `--dsw-alias-label-tertiary` | `#81858c` | `#adb2b8` |
| `--dsw-alias-link` | `#4176e6` | `#679efe` |

### Brand / accent
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-brand-primary` | `#0f1115` | `#f9fafb` |
| `--dsw-alias-brand-primary-invert` | `#0f1115` | `#f9fafb` |
| `--dsw-alias-brand-primary-new-colorprimary-new-color` | `#4176e6` | `#5686fe` |
| `--dsw-alias-brand-text` | `#0f1115` | `#f9fafb` |
| `--dsw-alias-state-business-primary` | `#4176e6` | `#679efe` |
| `--dsw-alias-state-business-tertiary` | `#e4edfd` | `#34415b` |

### Buttons
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-button-contrast-fill` | `#61666b` | `#f9fafb` |
| `--dsw-alias-button-elevated-fill` | `#fff` | `#43454a` |
| `--dsw-alias-button-floating-fill` | `#fff` | `#2c2c2e` |
| `--dsw-alias-button-floating-hover` | `#f1f3f5` | `#353638` |
| `--dsw-alias-button-ghost-active-border` | `#979da6` | `#81858c` |
| `--dsw-alias-button-ghost-active-fill` | `#ebeef2` | `#43454a` |
| `--dsw-alias-button-ghost-active-hover` | `#e9ecf2` | `#61666b` |
| `--dsw-alias-button-info-fill` | `#4176e6` | `#679efe` |
| `--dsw-alias-button-info-hover` | `#679efe` | `#4176e6` |
| `--dsw-alias-button-primary-dimmed` | `#ebeef2` | `#43454a` |
| `--dsw-alias-button-primary-fill` | `var(--dsw-alias-brand-primary)` (shared) | `var(--dsw-alias-brand-primary)` |
| `--dsw-alias-button-primary-hover` | `#43454a` | `#ebeef2` |
| `--dsw-alias-button-tool-bar-fill` | `#54555780` (shared) | `#54555780` |
| `--dsw-alias-button-tool-bar-fill-invisible` | `#1f1f1f5c` (shared) | `#1f1f1f5c` |
| `--dsw-alias-button-tool-bar-hover` | `#54555799` (shared) | `#54555799` |

### Interactive / state
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-interactive-bg-active` | `#2631481a` | `#ffffff24` |
| `--dsw-alias-interactive-bg-hover` | `#2631480f` | `#ffffff14` |
| `--dsw-alias-interactive-bg-hover-accent` | `#26314824` | `#ffffff3d` |
| `--dsw-alias-interactive-bg-hover-danger` | `#ec13130d` | `#f25a5a26` |
| `--dsw-alias-interactive-bg-hover-solid` | `#f1f3f5` | `#353638` |
| `--dsw-alias-state-error-primary` | `#ec1313` | `#f25a5a` |
| `--dsw-alias-state-error-secondary` | `#f25a5a` (shared) | `#f25a5a` |
| `--dsw-alias-state-success-primary` | `#22c55e` (shared) | `#22c55e` |
| `--dsw-alias-state-success-secondary` | `#4ed17e` (shared) | `#4ed17e` |
| `--dsw-alias-state-success-tertiary` | `#e6faed` | `#233c2c` |
| `--dsw-alias-state-warn-label` | `#dd8629` (shared) | `#dd8629` |
| `--dsw-alias-state-warn-primary` | `#f59e0b` (shared) | `#f59e0b` |
| `--dsw-alias-state-warn-secondary` | `#f7ad31` (shared) | `#f7ad31` |
| `--dsw-alias-state-warn-tertiary` | `#fef5e7` | `#27241f` |

### Markdown / code
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-markdown-citation` | `#ebeef2` | `#353638` |
| `--dsw-alias-markdown-code-block` | `#f9fafb` | `#1b1b1c` |
| `--dsw-alias-markdown-code-block-banner` | `#f9fafb` | `#2c2c2e` |
| `--dsw-alias-markdown-code-segment-selected` | `#fff` | `#353638` |
| `--dsw-alias-markdown-code-segment-unselected` | `#f1f3f5` | `#1b1b1c` |
| `--dsw-alias-markdown-inline-code` | `#fafafa` | `#292929` |
| `--dsw-alias-markdown-placeholder` | `#f5f6f7` | `#2c2c2e` |
| `--dsw-alias-markdown-tag` | `#f1f3f5` | `#2c2c2e` |

### Scrollbar
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-scrollbar-bg-l1` | `#e5e5e5` | `#3c3c3d` |
| `--dsw-alias-scrollbar-bg-l2` | `#e5e5e5` | `#545557` |
| `--dsw-alias-scrollbar-hover-l1` | `#d4d4d4` | `#545557` |
| `--dsw-alias-scrollbar-hover-l2` | `#d4d4d4` | `#65676b` |

### Floats
| Token | Light | Dark |
|---|---|---|
| `--dsw-alias-toast-bg` | `#353638` | `#43454a` |
| `--dsw-alias-tooltip-bg` | `#2c2c2e` | `#43454a` |

### Specific (local semantic)
| Token | Light | Dark |
|---|---|---|
| `--dsw-specific-bubble` | `#edf3fe` | `#2c2c2e` |
| `--dsw-specific-bubble-highlight` | `#d3e2ff` | `#43454a` |
| `--dsw-specific-input-major` | `#fff` | `#2c2c2e` |
| `--dsw-specific-login-input` | `#f9fafb` | `#1b1b1c` |
| `--dsw-specific-menu` | `var(--dsw-alias-bg-layer-3)` (shared) | `var(--dsw-alias-bg-layer-3)` |
| `--dsw-specific-selector` | `#f5f6f7` | `#353638` |
| `--dsw-specific-sidebar-fill` | `#f9fafb` | `#1b1b1c` |
| `--dsw-specific-sidebar-nav-item-active` | `#ebeef2` | `#43454a` |
| `--dsw-specific-sidebar-nav-item-active-accent` | `#e4edfd` | `#353638` |
| `--dsw-specific-sidebar-nav-item-hover` | `#f1f3f5` | `#2c2c2e` |
| `--dsw-specific-tip` | `#f5f6f7` | `#353638` |

> **Design-intent vs. actual**: the 3 `sidebar-nav-item-*` tokens exist but components do **not** use them — they use `--dsw-alias-interactive-bg-hover` / `--dsw-alias-interactive-bg-active`. Use the component-measured values when replicating; keep the specific tokens only as design intent. Source `dsh-client-ui-theme/lib/client.js:1052` (token table), §5.7.

---

## 3. Font tokens

### Families (on `:root`, `base.css`, `dsh-client-ui-theme/lib/client.js:1046`)
```css
--dsw-font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC",
                   "Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Helvetica, Arial, sans-serif;
--ds-font-family-code: "SF Mono", "JetBrains Mono", "Fira Code", Consolas,
                       "Liberation Mono", Menlo, Courier, "PingFang SC", "Microsoft YaHei";
```
> Note the prefix asymmetry: UI family is `--dsw-font-family` (three w's), code family is `--ds-font-family-code` (two w's). Copy as-is.

Applied on `body`: `font-family: var(--dsw-font-family, …)`, `-webkit-font-smoothing: antialiased`, `-moz-osx-font-smoothing: grayscale`, `color: var(--dsw-alias-label-primary, #0f1115)`, `background: var(--dsw-alias-bg-base, #fff)`. `button,input,select,textarea { font-family: inherit }`. Source `index-DPX2bQLO.css` tail (offset 51399–51947).

### Size tokens (`--dsw-font-*`, from `dsh-client-ui-theme/lib/client.js:1058`)
Each group is a `font` shorthand + weight components. Let `D = var(--dsh-content-font-delta)`, `S2 = var(--dsh-content-font-size-secondary)`, `D2 = var(--dsh-content-font-delta-secondary)`, `F = var(--dsw-font-family)`, `C = var(--ds-font-family-code)`. Defaults shown at 14px content font.

**Markdown ladder (linked to content font size):**
| Token | font shorthand | default |
|---|---|---|
| `--dsw-font-markdown-h1` | `700 calc(21px + D)/calc(30px + D) F` | 21/30 · 700 |
| `--dsw-font-markdown-h2` | `700 calc(19px + D)/calc(28px + D) F` | 19/28 · 700 |
| `--dsw-font-markdown-h3` | `700 calc(18px + D)/calc(26px + D) F` | 18/26 · 700 |
| `--dsw-font-markdown-h4` | `600 var(--dsh-content-font-size,14px)/calc(24px + D) F` | 14/24 · 600 |
| `--dsw-font-markdown-base` | `var(--dsh-content-font-size,14px)/calc(24px + D) F` | 14/24 · 400 |
| `--dsw-font-markdown-base-strong` | same + `600` | 14/24 · 600 |
| `--dsw-font-markdown-base-italic` | same + `italic` | 14/24 400 italic |
| `--dsw-font-markdown-base-strong-italic` | same + `italic 600` | 14/24 600 italic |
| `--dsw-font-markdown-table` | `S2/calc(22px + D2) F` | 13/22 · 400 |
| `--dsw-font-markdown-table-head` | `500 S2/calc(22px + D2) F` | 13/22 · 500 |
| `--dsw-font-markdown-small` (+`-strong/-italic/-strong-italic`) | `12px/20px F` (strong=600) | 12/20 **fixed** |
| `--dsw-font-markdown-code` | `12px/19px C` | 12/19 **fixed** |
| `--dsw-font-markdown-code-block` | `11px/19px C` | 11/19 **fixed** |
| `--dsw-font-markdown-code-block-small` | `11px/16px C` | 11/16 **fixed** |

**Fixed UI ladder (does not follow settings):**
| Token | font shorthand | default |
|---|---|---|
| `--dsw-font-xl-24` | `600 24px/32px F` | 24/32 · 600 |
| `--dsw-font-l-20` | `500 20px/28px F` | 20/28 · 500 |
| `--dsw-font-m-18` | `500 16px/28px F` (name says 18, size is really 16) | 16/28 · 500 |
| `--dsw-font-base-16` / `-strong-16` | `16px/24px F` / `500 16px/24px F` | 16/24 |
| `--dsw-font-s-14` / `-strong-14` | `14px/22px F` / `500 14px/22px F` | 14/22 |
| `--dsw-font-xs-13` / `-strong-13` | `13px/20px F` / `500 13px/20px F` | 13/20 |
| `--dsw-font-xxs-12` / `-strong-12` | `12px/18px F` / `500 12px/18px F` | 12/18 |
| `--dsw-font-xxxs-11` / `-strong-11` | `11px/14px F` / `500 11px/14px F` | 11/14 |

> ⚠️ Known defect (copy as-is, don't fix): `ToolRow.module.css` references `--dsw-font-sm-13`, which is **never defined** anywhere — that declaration is dead. Use `--dsw-font-xs-13` (13/20) instead. Source §1.4.

---

## 4. Spacing / radius / duration

### Motion tokens (`--ds-*`, `:root`, `base.css`, `dsh-client-ui-theme/lib/client.js:1046`)
```css
--ds-ease-in-out: cubic-bezier(.4, 0, .2, 1);
--ds-transition-duration: .2s;
--ds-transition-duration-fast: .1s;
--ds-transition-duration-slow: .3s;
```
Other hardcoded durations (corpus counts): `.12s`(35) / `.18s`(15) / `.1s`(9) / `.16s`(9) / `.15s`(8) / `.14s`(6) / `.2s`(6) / `2.6s`(5, tool-row running sweep) / `.8s`(3, spinner) / `1.6s`(2) / `1.8s`(1, turn-status shimmer) / `80ms`(1, message-action reveal).
Add `@media (prefers-reduced-motion: reduce){ … transition:none; animation:none }` on every moving component (a11y convention). Source §1.6.

### Spacing
**No spacing-scale tokens exist** (`--spacing`/`--gap-*`/`--space-*` not found). Spaces are hardcoded per component. Common values (corpus): `4 / 6 / 8 / 10 / 12 / 14 / 16 / 20 / 24 px` (card padding `12px 16px`, grid gap `8px`). Source `docs/ui-tokens.md:350`.

### Radius
**Not tokenized** except component-local `--dsl-*-radius`. Frequency table (pick from these):
`2px` (build badge)· `3px` (copy btn/arrow)· `4px` (rename input/menu item)· `5px` (compact·menu item)· `6px` (inline code/call row/collapse)· `7px` (compact menu)· `8px` (sidebar/session row/icon btn· tooltip icon· tag)· `10px` (menu item)· `12px` (**all tool/code blocks** `--dsl-*-radius`, modal, hovercard, panel card)· `14px` (toast)· `16px` (FileCard)· `18px` (Button md 36px)· `20px` (Menu container, float panel)· `22px` (**user bubble, composer card**)· `24px` (Dialog)· `28px` (round icon button)· `999px`/`100px`/`50%` (capsule/send btn/state dot). Source §1.5.

### z-index (needed tiers only)
`1`(dialog content/divider/copy)· `7`(composer seat, `[data-phase=active]`)· `8`(back-to-bottom/drag handle)· `9`(composer seat w/ trigger)· `10`(right panel, dock)· `11`(sidebar drag handle)· `20`(shell overlay)· `40`(right panel fullscreen)· `100`(dropdown list, hovercard, tooltip)· `101`(submenu)· `1000`(modal mask)· `1100`(**top**: toast, portal menu, onboarding). Source §1.5.

### Elevation / shadow (`dsh-client-ui-theme/lib/client.js:1058`)
| Token | Light (`body`) | Dark |
|---|---|---|
| `--dsw-shadow-lv1` | `0 2px 4px 0 #0000000d` | same |
| `--dsw-shadow-lv1-blur` | `0 4px 12px 0 #00000005` | same |
| `--dsw-shadow-lv2` | `0 4px 12px 0 #00000005, 0 2px 8px 0 #0000000a` | same |
| `--dsw-shadow-lv3` | `0 0 1px 0 #0003, 0 0 4px 0 #00000005, 0 12px 32px 0 #00000014` | same |
| `--dsw-elevation-stroke-color` | `var(--dsw-alias-border-l4)` | same (rebind per element) |

**"Elevation trio"** on selector `body, body *`:
```css
body, body * {
  --dsw-elevation-stroke: 0 0 0 .5px var(--dsw-elevation-stroke-color);
  --dsw-elevation-panel: var(--dsw-elevation-stroke), 0 3px 8px 0 #00000008, 0 0 16px 0 #00000005;
  --dsw-elevation-prominent: var(--dsw-elevation-stroke), 0 3px 8px 0 #0000000a, 0 0 20px 0 #0000000d;
  --dsw-elevation-soft: var(--dsw-elevation-stroke), 0 4px 16px 0 #00000008, 0 0 24px 0 #00000008; /* input bar: larger blur, lower alpha */
}
```
High-level surfaces use `border:0; box-shadow: var(--dsw-elevation-panel)` instead of layout borders. Rebinding examples: menu → `--dsw-elevation-stroke-color: var(--dsw-alias-border-l1)`; composer card → `var(--dsw-alias-border-l2)`. Source §1.3.

### Scrollbar
```css
body {
  --dsh-scrollbar-thumb: var(--dsw-alias-scrollbar-bg-l1);        /* light #e5e5e5 / dark #3c3c3d */
  --dsh-scrollbar-thumb-hover: var(--dsw-alias-scrollbar-hover-l1);/* light #d4d4d4 / dark #545557 */
  --dsh-scrollbar-width: 8px;
}
::-webkit-scrollbar { width:8px; height:8px }
::-webkit-scrollbar-track { background:transparent }
::-webkit-scrollbar-thumb { background:var(--dsh-scrollbar-thumb); border-radius:4px }
::-webkit-scrollbar-thumb:hover { background:var(--dsh-scrollbar-thumb-hover) }
::-webkit-scrollbar-corner { background:transparent }
@supports not selector(::-webkit-scrollbar) { body, body * { scrollbar-width:thin; scrollbar-color:var(--dsh-scrollbar-thumb) transparent } }
```
Rebind to `l2` tier on high-level surfaces (menu, dialog, composer card, preview panel) and to `transparent` on sidebar when not hovered (`--dsh-scrollbar-thumb:transparent`). Source §6.

---

## 5. Layout spec

### 5.1 Three-column grid (`AppFrame.module.css`, `dsh-client-ui-layout/lib/client.js:70`)
```css
.frame { background:var(--dsw-alias-bg-base); height:100%;
  display:grid; grid-template-rows:100%;
  grid-template-columns: <sidebar>px minmax(0, 1fr) <rightbar>px; /* JS-inlined */
  transition: grid-template-columns var(--ds-transition-duration-slow) var(--ds-ease-in-out);
  position:relative; overflow:hidden }
.sidebarCol { background:var(--dsw-specific-sidebar-fill); /* light #f9fafb / dark #1b1b1c */
  border-right:.5px solid var(--dsw-alias-border-l3); min-width:0; overflow:hidden }
.centerCol  { display:flex; flex-direction:column; min-width:0; overflow:hidden }
.rightbarCol{ min-width:0; position:relative; overflow:visible }
.handle     { width:8px; margin-left:-4px; cursor:col-resize; z-index:11;
              position:absolute; top:0; bottom:0 }
```

**Size constants (JS, authoritative):**
| Quantity | Value | Source |
|---|---|---|
| Sidebar default width | **280 px** | `dsh-client-ui-layout/lib/client.js:343` (`sidebar: 280`) |
| Sidebar drag range | **264 – 420 px** | `:39` `clampWidth(sidebar,264,420)`, `:362` |
| Sidebar collapsed (rail) width | **56 px** | `:38` `const s = sidebar===0 ? 56 : clampWidth(sidebar,264,420)` |
| Auto-collapse breakpoint | viewport **< 1024 px** | `:13` `SIDEBAR_AUTO_COLLAPSE=1024`, `:233` |
| Center min available width | 400 px | `:39` (`available = viewport - s - 400`) |
| Right bar min / default / max | min **300 px**; first open = viewport × **0.45**; max = viewport × **0.7** | `:15` `RIGHTBAR_MAX_RATIO=.7`, `:17` `RIGHTBAR_DEFAULT_RATIO=.45`, `:40` |
| Column transition | `grid-template-columns .3s cubic-bezier(.4,0,.2,1)`, off while `[data-dragging]` | §8.4 |

### 5.2 Sidebar internals (`SidebarRoot.module.css`, `dsh-client-ui-sidebar/lib/client.js:27`)
| Part | Spec |
|---|---|
| Root | `padding:6px var(--dsh-sidebar-inline-padding=12px)`; `background:var(--dsw-specific-sidebar-fill)`; `font-size:14px`; collapsed `padding:18px 10px 6px` |
| Logo row | `height:60px`, `margin-bottom:8px`, `padding:8px 0 8px 4px`; collapsed 36px / `margin-bottom:12px` / padding 0 |
| Brand name | `font-size:18px / line-height:24px / font-weight:600 / letter-spacing:.04em` |
| New-session button | `height:38px`, `border-radius:12px`, `.5px solid var(--dsw-alias-border-l3)`, `background:var(--dsw-alias-button-elevated-fill)`, hover `--dsw-alias-button-floating-hover`, `padding:8px 16px`, 14/22/500; collapsed 36×36 transparent no border |
| Panel nav row | `min-height:36px`, `padding:7px 8px`, `border-radius:8px`, `color:var(--dsw-alias-label-secondary)`; hover `--dsw-alias-interactive-bg-hover`; active `--dsw-alias-interactive-bg-active` + `color:label-primary` + `font-weight:500`; focus-visible `outline:2px solid var(--dsw-alias-label-primary); outline-offset:-2px`; collapsed 36×36 centered |
| Icon button | 28×28 (collapsed 36×36), `border-radius:50%; corner-shape:round`, `color:label-secondary`, hover `--dsw-alias-interactive-bg-hover` |
| Animations | expand `wide-in .2s`; rail-in `.15s`; `COLLAPSE_SETTLE_MS=150` |

Session rows (`Rows.module.css`, `ui-workspace/lib/client.js:554`): session row `height:32px`, `border-radius:8px`, `padding:0 8px`; project row `height:34px`, `gap:6px`; hover/selected/menuOpen all `background:var(--dsw-alias-interactive-bg-hover)`; title 14/20 single-line ellipsis; time 12/20 `label-tertiary`; search-result row `min-height:48px`.

### 5.3 Main conversation area (`ConversationRoot.module.css`, `ui-conversation/lib/client.js:14651`)
| Item | Value |
|---|---|
| Content column width `--dsh-chat-content-width` | `clamp(680px, 列宽 × 0.64, 920px)` = `clamp(680px, calc(var(--dsh-conversation-column-width,0px) * .64), 920px)`; JS-driven via ResizeObserver writing `--dsh-chat-user-width` |
| Conversation row padding | `padding:16px calc(var(--dsh-composer-side-clearance) + 16px)` = **16px 32px** (`ui-chat/lib/client.js:1507`); column `max-width:var(--dsh-chat-content-width); margin:0 auto` |
| Message-flow gap | adjacent non-empty child `margin-top: var(--dsh-chat-flow-gap, 16px)`; **8px** on `[data-turn-process-answer]` |
| Header | `min-height:76px`, `padding:10px 28px 0 20px`, `border-bottom:.5px solid var(--dsw-alias-border-l3)`; title `min-height:30px`; breadcrumb 14/20 `max-width:220px` `border-radius:12px` `padding:4px 8px` |
| Header tabs | `gap:36px`, `margin-top:10px`, 13/16/500, active `--dsw-alias-state-business-primary`, underline `height:2px; bottom:-1px; border-radius:2px` |
| Composer dock | `position:sticky; bottom:0; z-index:7`; bg `linear-gradient(180deg, color-mix(… 0%, transparent) 0px, var(--dsw-alias-bg-base) 36px)` (36px top fade) |
| Hero (empty) state | headline `font-size:26px / line-height:32px / font-weight:500`; workspace selector `min-height:28px` 13/20/500 `border-radius:16px` `max-width:min(100%,360px)` (`/HeroShell`, `:14498`) |

### 5.4 Input bar / composer (`InputBar.module.css`, `ui-conversation/lib/client.js:15756`)
| Element | Spec |
|---|---|
| Outer `.root` | `padding:0 var(--dsh-composer-side-clearance=16px) 8px`; centered column |
| **Card `.card`** | `max-width: var(--dsh-composer-card-max-width)` (= content column width + 32px); `background: var(--dsw-specific-input-major)` (light `#fff` / dark `#2c2c2e`); **`border:0` + `box-shadow: var(--dsw-elevation-soft)`** with `--dsw-elevation-stroke-color: var(--dsw-alias-border-l2)`; **`border-radius:22px`**; `padding-top:8px`; `gap:12px`; `font-size:var(--dsh-content-font-size,14px)`; `line-height:calc(24px + D)` |
| Textarea | `min-height:36px` (hero 52px); `padding:4px 8px 0 14px`; `font-family:var(--dsw-font-family)`; `color:var(--dsw-alias-label-primary)`; **`caret-color:var(--dsw-alias-state-business-primary)`**; `outline:none`; `white-space:pre-wrap; word-break:break-word; overflow-wrap:anywhere` |
| Text max-height | `.scroll { max-height: var(--dsh-composer-text-max-height=336px) }` |
| Focus state | **No `:focus-within` highlight** (deliberate — layering comes from persistent `elevation-soft`). Focus feedback = caret color only. System focus pattern elsewhere: `:focus-visible { outline:2px solid var(--dsw-alias-label-primary); outline-offset:-2px }` or `box-shadow:0 0 0 2px var(--dsw-alias-state-business-primary)` |
| Bottom row | `padding:2px 8px 6px; gap:12px; flex-wrap:wrap; container-type:inline-size` |
| Add button | 28×28, `border-radius:999px; corner-shape:round`, `background:var(--dsw-specific-selector)` (light `#f5f6f7` / dark `#353638`), `color:label-primary`; hover `--dsw-alias-interactive-bg-hover-solid`; disabled `opacity:.5` |
| Model/permission select | `height:28px`, `max-width:220px`, `border-radius:8px`, 13/20/500, `color:label-secondary`, `padding:0 20px 0 8px`; hover `interactive-bg-hover` |
| **Send button `.primary`** | **34×34**; `border-radius:999px; corner-shape:round`; `background:var(--dsw-alias-button-info-fill)` (light `#4176e6`=deepseek-500 / dark `#679efe`=deepseek-400); `color:#fff`; `transition:background-color .1s`; hover `--dsw-alias-button-info-hover` (light `#679efe` / dark `#4176e6`); disabled `opacity:.4`; `transform:translateY(-2px)` (visually floats) |
| Notice bar | `background:var(--dsw-alias-interactive-bg-hover)`; `color:label-secondary`; `border-radius:8px`; `padding:4px 8px`; 12/18; `margin-bottom:6px` |

**Button primitive** (`index-DPX2bQLO.css` offset 4258–5384): base `inline-flex; align-items:center; justify-content:center; gap:4px; border:none; border-radius:18px; font-size:14px; line-height:22px; color:label-primary; background:transparent; padding:0 14px`; `_md` 36px; `_sm` height28px, 12/18, `padding:0 10px`, `border-radius:14px`; `_primary` `background:var(--dsw-alias-button-primary-fill); color:var(--dsw-alias-label-primary-foreground)`; hover `--dsw-alias-button-primary-hover`; `_outline` `.5px solid var(--dsw-alias-border-l3)`; `_toolbar` `background:var(--dsw-alias-button-tool-bar-fill)`; `:disabled { cursor:not-allowed; opacity:.4 }`.

### 5.5 Tool-call row (ToolRow) chrome (`ToolRow.module.css`, `ui-tool/lib/client.js:1143`)
**Collapsed (single line = one text line):**
| Part | Spec |
|---|---|
| Row shell | `height:calc(24px + D)` (default 24px); `overflow:hidden`; `[data-expandable] → cursor:pointer` |
| Leading icon | `calc(16px + D)` slot, `margin-right:6px`, `color:label-tertiary`; inner svg `calc(14px + D)` |
| Title | `font-weight:400`, 13px/`calc(24px + D)`, `color:label-secondary` |
| Separator dot `.sep` | `2×2px`, `border-radius:1px`, `background:var(--dsw-alias-label-caption)`, `margin:0 8px` |
| Summary | 13px/`calc(24px + D)`, `color:var(--dsw-alias-label-tertiary)`, single-line ellipsis |
| diff stat | `font-family:var(--ds-font-family-code)`; `font-size:calc(13px - 2px)` = **11px**; `color:label-caption`; `margin-left:10px; transform:translateY(.5px)` |
| File link | `text-decoration:underline dotted var(--dsw-alias-label-tertiary)`; `text-underline-offset:3px`; `text-decoration-thickness:1px`; hover `color:label-primary` |
| Error summary | `color: var(--dsw-alias-state-error-primary)` |
| **Running sweep** `[data-state=running] .row::after` | `width:300px`; `background:linear-gradient(90deg, transparent 0%, color-mix(in srgb, var(--dsw-alias-bg-base) 60%, transparent) 55%, transparent 100%)`; `animation:2.6s ease-out infinite` (left −300px → 100%) |
| Collapse button | chevron fades in on leading-icon hover; `[data-expandable]` cursor pointer (no dedicated positioned button — the whole collapsed row is the click target; expand = click `[data-expandable]`) |

**Expanded:**
| Part | Spec |
|---|---|
| Collapse container (ToolCallTree `:1431`) | `border-radius:6px` |
| Sub-call indent | `border-left:.5px solid var(--dsw-alias-border-l2)`; `margin:4px 0 2px 22px; padding-left:8px; gap:4px` |
| Output scroll | `.bodyScroll { max-height:260px; overflow-y:auto }`; each IO segment `max-height:150px` |
| IO card | `border:.5px solid var(--dsw-alias-border-l1)`; `background:var(--dsw-alias-markdown-code-block)` (light `#f9fafb` / dark `#1b1b1c`); `border-radius:12px`; `margin:4px 0 4px 4px`; `font:var(--dsw-font-markdown-code-block-small)` (11/16 code) |
| IO grid | `grid-template-columns:max-content 1fr`; `column-gap:14px`; `padding:12px 16px`; label sticky `color:label-caption`; divider `.5px var(--dsw-alias-border-l2)`; text `label-secondary`, `[data-error] → state-error-primary` |
| Inspect capsule | default `opacity:0` → hover/focus `1` (`.1s`); `.5px solid var(--dsw-alias-border-l3)`; `background:var(--dsw-alias-bg-base)`; `border-radius:999px; corner-shape:round`; 11/16 `padding:2px 8px`; `margin:4px 0 2px 4px`; hover `--dsw-alias-interactive-bg-hover-solid` |
| Default collapse threshold | diff/read/search/terminal each **16 lines** (`DEFAULT_*_MAX_LINES=16`) |

`--dsl-*` code/tool block locals (all in shared primitives, dist CSS): `--dsl-code-block-background: var(--dsw-alias-markdown-code-block)`; `--dsl-code-block-banner-background-color: var(--dsw-alias-markdown-code-block-banner)`; `--dsl-code-block-border-radius: 12px`; `--dsl-code-block-banner-font: 11px/18px var(--dsw-font-family)`; `--dsl-code-block-content-font: var(--dsw-font-markdown-code-block)` (11/19); ToolRow/CodeBody override content font to `…-small` (11/16). Terminal: `--dsl-terminal-radius 12px` / `line-height 22px` / `gutter 30px`; ToolRow `line-height:18px`, `font:…-small`, `--dsl-terminal-output-max-height:224px`. Read/diff/search/web radius all `12px`.

### 5.6 Messages (`MessageItem.module.css` / `ChatView.module.css`, `ui-chat/lib/client.js:154` / `:1507`)
| Element | Spec |
|---|---|
| **User bubble `.bubble`** | `background:var(--dsw-specific-bubble)` (light `#edf3fe` / dark `#2c2c2e`); `border-radius:22px`; `padding:10px 16px`; `font-size:var(--dsh-content-font-size,14px)`; `line-height:calc(22px + D)`; `color:var(--dsw-alias-label-primary)`; `white-space:pre-wrap; word-break:break-word` |
| User stack | `max-width:min(calc(var(--dsh-chat-content-width,748px) * .702), 82%)`; `gap:8px`; row `gap:6px` flex-end |
| Assistant (no bubble) | `.hWmORq_root` `font-size:var(--dsh-content-font-size,14px); line-height:calc(24px + D); color:label-primary`; block gaps `16px` (AssistantMarkdown `:2933`) |
| Message gap | `--dsh-chat-flow-gap:16px` (`[data-turn-process-answer]` → 8px) |
| Back-to-bottom button | 34×34, `border-radius:100px`, `background:var(--dsw-alias-button-floating-fill)` (light `#fff` / dark `#2c2c2e`), `box-shadow:var(--dsw-elevation-panel)` + `--dsw-elevation-stroke-color:var(--dsw-alias-border-l3)`; dock `bottom:16px` (with composer `bottom:calc(var(--dsh-composer-height,152px) + 16px)`) |
| Row action buttons | `calc(28px + D)` square, `border-radius:28px`, `padding:6px`, icon `calc(15px + D)`, `color:label-tertiary`; hover `interactive-bg-hover` + `label-secondary`; default `opacity:0` → `:hover/:focus-within` `opacity:1` (`.1s`/80ms) within `@media (hover:hover)` (MessageIconActions `:1003`) |

---

## 6. Extra reference (shiki syntax colors — optional)
Custom `css-variables` theme, prefix `--shiki-`, mounted on `:root` (light) with `body[data-ds-dark-theme]` overrides (hardcoded literals; not in the alias system). `--shiki-foreground` = `var(--dsw-alias-label-primary)` and `--shiki-background` = `var(--dsw-alias-markdown-code-block)` (theme-linked), so flip requires no re-highlight.
| token | light | dark |
|---|---|---|
| `--shiki-token-constant` | `#1c7ed6` | `#4dabf7` |
| `--shiki-token-string` | `#2f9e44` | `#69db7c` |
| `--shiki-token-comment` | `#868e96` | `#adb5bd` |
| `--shiki-token-keyword` | `#d6336c` | `#faa2c1` |
| `--shiki-token-parameter` | `#e8590c` | `#ffa94d` |
| `--shiki-token-function` | `#6741d9` | `#b197fc` |
| `--shiki-token-string-expression` | `#2b8a3e` | `#8ce99a` |
| `--shiki-token-punctuation` | `#495057` | `#ced4da` |
| `--shiki-token-link` | `#1971c2` | `#74c0fc` |

---

## 7. Source line map
- Theme switch: `docs/ui-tokens.md:54–62`, `dsh-client-ui-layout/lib/client.js:443,464–472`
- Static palette: `docs/ui-tokens.md:86–167`, `dsh-client-ui-theme/lib/client.js:1052`
- Alias/specific table: `docs/ui-tokens.md:171–278`, `dsh-client-ui-theme/lib/client.js:1052`
- Shadows/elevation: `docs/ui-tokens.md:282–308`, `:1058`
- Font families: `docs/ui-tokens.md:488–528`, `:1046`
- Font ladder: `docs/ui-tokens.md:310–346`, `:1058`
- Motion tokens: `docs/ui-tokens.md:395–408`, `:1046`
- Radius/spacing/z-index: `docs/ui-tokens.md:348–393`
- Layout grid: `docs/ui-tokens.md:535–568`, `dsh-client-ui-layout/lib/client.js:70`
- Sidebar: `docs/ui-tokens.md:570–580`, `dsh-client-ui-sidebar/lib/client.js:27`
- Rows: `docs/ui-tokens.md:582–594`, `dsh-client-ui-workspace/lib/client.js:554`
- Conversation area: `docs/ui-tokens.md:596–612`, `ui-conversation/lib/client.js:14651`
- Composer / input bar: `docs/ui-tokens.md:777–814`, `ui-conversation/lib/client.js:15756`
- ToolRow: `docs/ui-tokens.md:663–711`, `ui-tool/lib/client.js:1143`
- Messages: `docs/ui-tokens.md:639–661`, `ui-chat/lib/client.js:154,1507`
- Code block: `docs/ui-tokens.md:713–751`, dist CSS offset 31450–33862
