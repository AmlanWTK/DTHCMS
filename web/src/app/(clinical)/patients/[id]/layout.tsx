import type { ReactNode } from 'react';

import { PatientHeader } from '@/features/patients';

/**
 * The shell every screen about one patient sits inside (CP54, acceptance criterion 3).
 *
 * The criterion is "allergies appear on the patient header on every screen", and the only
 * way to make that true of screens nobody has written yet is to hang the header off the
 * path rather than off each page. Every present and future route under `/patients/[id]`
 * inherits it here; a banner added page by page is a banner missing from the page built
 * next month, which is as likely as not the one where somebody writes a prescription.
 */
export default async function PatientLayout({
  children,
  params,
}: {
  children: ReactNode;
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;

  return (
    <div className="app-stack">
      <PatientHeader patientId={id} />
      {children}
    </div>
  );
}
