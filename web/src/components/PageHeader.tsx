import type { ReactNode } from 'react';

/** The title block every screen opens with. One element, so they cannot drift apart. */
export function PageHeader({ title, description }: { title: ReactNode; description?: ReactNode }) {
  return (
    <div>
      <h1 className="app-page__title">{title}</h1>
      {description && <p className="app-page__description">{description}</p>}
    </div>
  );
}
