import { AccountConsole } from '@/features/users';

/**
 * Administration → Users → one account (CP21). The header is the account's own name, so
 * the console draws it once it has loaded.
 */
export default async function AccountPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <AccountConsole id={id} />;
}
