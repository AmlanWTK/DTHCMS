'use client';

import { useTranslations } from 'next-intl';
import { useEffect, useRef, useState } from 'react';

import { Button } from '@dthcms/ui';

import { draw, hasMark, type Stroke } from '../lib/signature';

/**
 * A place to sign or press a thumb (CP36).
 *
 * Pointer events rather than mouse and touch separately: a clinic runs on tablets with
 * styluses and fingers and the occasional desk mouse, and three code paths for one gesture is
 * three places for the last stroke to go missing.
 *
 * The ink is **always black on white** in the exported PNG, whatever the interface theme. A
 * signature rendered in the dark palette is a white mark on transparency, which prints as
 * nothing — and this image is the one thing here that might be printed.
 */
export function SignaturePad({
  onChange,
  label,
  disabled,
}: {
  /** Called with a PNG when the mark changes, or null when it is cleared. */
  onChange: (png: Blob | null) => void;
  label: string;
  disabled?: boolean;
}) {
  const t = useTranslations('patients.consent');
  const canvas = useRef<HTMLCanvasElement>(null);
  const [strokes, setStrokes] = useState<Stroke[]>([]);
  const drawing = useRef(false);
  // Held in a ref so the export effect depends on the strokes alone. A parent that passes a
  // fresh closure on every render would otherwise re-export the signature on every render.
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const WIDTH = 520;
  const HEIGHT = 180;

  useEffect(() => {
    const element = canvas.current;
    if (!element) return;
    // Guarded: a 2D context is unavailable in a few environments — a headless renderer, a
    // browser with canvas disabled — and there the pad is simply not usable rather than a
    // crash that takes the consent screen with it.
    let ctx: CanvasRenderingContext2D | null = null;
    try {
      ctx = element.getContext('2d');
    } catch {
      return;
    }
    if (!ctx) return;
    draw(ctx, strokes, { width: WIDTH, height: HEIGHT, colour: '#111111' });
  }, [strokes]);

  // Exporting is an effect of the strokes changing, not something the pointer handler does.
  // Doing it inside a state updater looked simpler and was wrong: an updater must be pure,
  // and React is free to call it more than once — which exports the same signature twice and,
  // worse, exports it before the effect above has drawn the last stroke onto the canvas.
  useEffect(() => {
    const element = canvas.current;
    if (!element || !hasMark(strokes)) {
      onChangeRef.current(null);
      return;
    }
    let live = true;
    element.toBlob((blob) => {
      if (live) onChangeRef.current(blob);
    }, 'image/png');
    return () => {
      live = false;
    };
  }, [strokes]);

  function pointFrom(event: React.PointerEvent<HTMLCanvasElement>) {
    const box = event.currentTarget.getBoundingClientRect();
    return {
      x: ((event.clientX - box.left) / box.width) * WIDTH,
      y: ((event.clientY - box.top) / box.height) * HEIGHT,
    };
  }

  return (
    <div className="app-sign">
      <label htmlFor="signature-pad">{label}</label>
      <canvas
        id="signature-pad"
        data-testid="signature-pad"
        ref={canvas}
        width={WIDTH}
        height={HEIGHT}
        className="app-sign__pad"
        aria-label={label}
        style={disabled ? { opacity: 0.5, pointerEvents: 'none' } : undefined}
        onPointerDown={(event) => {
          event.currentTarget.setPointerCapture(event.pointerId);
          drawing.current = true;
          setStrokes((current) => [...current, [pointFrom(event)]]);
        }}
        onPointerMove={(event) => {
          if (!drawing.current) return;
          const point = pointFrom(event);
          setStrokes((current) => {
            const next = current.slice();
            const last = next[next.length - 1];
            if (last) next[next.length - 1] = [...last, point];
            return next;
          });
        }}
        onPointerUp={(event) => {
          drawing.current = false;
          event.currentTarget.releasePointerCapture(event.pointerId);
        }}
      />
      <div className="app-sign__actions">
        <Button
          variant="quiet"
          onClick={() => {
            setStrokes([]);
            onChange(null);
          }}
          disabled={disabled || strokes.length === 0}
        >
          {t('clear')}
        </Button>
        <p className="app-sign__hint">{t('padHint')}</p>
      </div>
    </div>
  );
}
