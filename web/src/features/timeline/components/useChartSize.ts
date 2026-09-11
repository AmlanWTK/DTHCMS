'use client';

import { useEffect, useRef, useState, type RefObject } from 'react';

/**
 * How wide the chart may draw, measured rather than assumed.
 *
 * A time axis is the one chart element that cannot have a fixed width: the whole interaction
 * is "how much of a decade fits on screen", and a viewBox scaled to fit would make a decade
 * on a phone and a decade on a desktop the same picture at two sizes — which is exactly the
 * property this screen must not have, because the number of pixels per month is what decides
 * whether two events read as related.
 *
 * `ResizeObserver` rather than a window resize listener: this element sits inside a grid
 * whose columns change without the window changing, and a window listener never fires for
 * that. The height is not observed — it is chosen from the lane count, which is data.
 *
 * The fallback of 0 is deliberate. A first render before the observer has fired must draw
 * **nothing** rather than a chart at a guessed width that then jumps; the caller checks for
 * it. In jsdom, where layout reports zero for everything, the caller uses the same check to
 * draw its text alternative — which is what makes this component testable without a stubbed
 * layout engine.
 */
export function useChartSize(): [RefObject<HTMLDivElement | null>, number] {
  const ref = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(0);

  useEffect(() => {
    const element = ref.current;
    if (element === null) return;

    // Measured once immediately as well as on change: an element that is already its final
    // size before the observer's first callback would otherwise render at zero for one
    // frame, and one blank frame on a slow tablet reads as a screen that failed.
    const measure = () => setWidth(element.getBoundingClientRect().width);
    measure();

    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  return [ref, width];
}
