import { Skeleton } from '@dthcms/ui';

/**
 * The loading state.
 *
 * Skeletons rather than a spinner. A spinner says "something is happening"; a skeleton
 * says "something is happening and here is its shape", which on a slow clinic connection
 * is the difference between waiting and wondering whether to reload.
 *
 * @dthcms/ui's Skeleton announces itself to a screen reader, which a bare grey rectangle
 * does not.
 */
export default function Loading() {
  return (
    <div className="app-stack">
      <Skeleton shape="block" width="18rem" height="var(--space-8)" />
      <Skeleton shape="block" width="100%" height="var(--space-24)" />
      <Skeleton shape="block" width="100%" height="var(--space-24)" />
    </div>
  );
}
