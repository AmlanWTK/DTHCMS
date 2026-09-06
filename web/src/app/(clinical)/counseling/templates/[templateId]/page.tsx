import { TemplateWorkspace } from '@/features/counseling';

/**
 * Clinical → Counselling checklists → one checklist (CP55).
 *
 * The header is the template's own title, so the workspace draws it once the listing has
 * loaded — the same shape as the account console at CP21.
 */
export default async function CounselingTemplatePage({
  params,
}: {
  params: Promise<{ templateId: string }>;
}) {
  const { templateId } = await params;
  return <TemplateWorkspace templateId={templateId} />;
}
