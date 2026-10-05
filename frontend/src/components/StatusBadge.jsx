// A small coloured pill for a run/shard status. The colour comes from CSS classes
// badge-queued / badge-running / badge-completed / badge-failed / badge-cancelling
// / badge-cancelled.
export default function StatusBadge({ status }) {
  return <span className={`badge badge-${status}`}>{status}</span>;
}
