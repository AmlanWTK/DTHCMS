'use client';

/**
 * The last boundary.
 *
 * This one replaces the root layout, which means no providers, no next-intl and no
 * knowledge of which language the person reads. Every other error page can look the
 * locale up; this one cannot.
 *
 * So it shows both languages at once. That is not a compromise — it is the only correct
 * answer when the failure is severe enough to have taken the language machinery with it,
 * and it is also why the text here is hard-coded rather than imported: whatever broke may
 * be the message loader.
 *
 * Styling is inline for the same reason. If the stylesheet is what failed, a page that
 * depends on it renders as unstyled text on white — which is legible, but the reference
 * a person needs to quote would be lost in it.
 */
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const reference = error.digest ?? 'unavailable';

  return (
    <html lang="en">
      <body
        style={{
          margin: 0,
          minHeight: '100dvh',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '1.5rem',
          fontFamily: 'system-ui, sans-serif',
          background: '#ffffff',
          color: '#111111',
        }}
      >
        <main style={{ maxWidth: '34rem', display: 'grid', gap: '1.25rem' }}>
          <section lang="en">
            <h1 style={{ fontSize: '1.5rem', margin: '0 0 0.5rem' }}>Something went wrong</h1>
            <p style={{ margin: 0 }}>
              The application could not recover. Nothing you entered has been sent to the clinic
              record.
            </p>
          </section>

          <section lang="bn">
            <h2 style={{ fontSize: '1.5rem', margin: '0 0 0.5rem', lineHeight: 1.7 }}>
              কিছু একটা সমস্যা হয়েছে
            </h2>
            <p style={{ margin: 0, lineHeight: 1.9 }}>
              অ্যাপ্লিকেশনটি আর চালু রাখা যায়নি। আপনি যা লিখেছেন তার কিছুই ক্লিনিকের রেকর্ডে পাঠানো
              হয়নি।
            </p>
          </section>

          <p style={{ margin: 0 }}>
            <strong>Reference / রেফারেন্স:</strong>{' '}
            <code style={{ userSelect: 'all' }}>{reference}</code>
          </p>

          <button
            type="button"
            onClick={reset}
            style={{
              minHeight: '3rem',
              padding: '0 1.25rem',
              fontSize: '1rem',
              cursor: 'pointer',
              borderRadius: '0.5rem',
              border: '1px solid #111111',
              background: '#111111',
              color: '#ffffff',
            }}
          >
            Try again / আবার চেষ্টা করুন
          </button>
        </main>
      </body>
    </html>
  );
}
