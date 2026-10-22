/** Value of a design token (CSS custom property on :root), e.g. cssVar('--accent'). */
export function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}
