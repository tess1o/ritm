/** Horizontal swipe detection for prev/next navigation (UI-M3). */
export function useSwipe(onLeft: () => void, onRight: () => void, threshold = 60) {
  let x0 = 0
  let y0 = 0
  function onTouchStart(e: TouchEvent) {
    x0 = e.changedTouches[0].clientX
    y0 = e.changedTouches[0].clientY
  }
  function onTouchEnd(e: TouchEvent) {
    // ignore swipes that start on charts (they handle their own touch)
    if ((e.target as HTMLElement).closest('canvas')) return
    const dx = e.changedTouches[0].clientX - x0
    const dy = e.changedTouches[0].clientY - y0
    if (Math.abs(dx) < threshold || Math.abs(dx) < Math.abs(dy) * 1.5) return
    if (dx < 0) onLeft()
    else onRight()
  }
  return { onTouchStart, onTouchEnd }
}

/** Swipe left/right on a period page to move to the next/previous period (UI-M3). */
export function usePeriodSwipe(shift: (direction: -1 | 1) => void, canGoForward: () => boolean) {
  return useSwipe(
    () => canGoForward() && shift(1),
    () => shift(-1),
  )
}
