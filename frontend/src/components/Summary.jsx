import { IconLayers, IconPlay, IconCheck, IconX } from "./icons.jsx";

// Four at-a-glance tiles computed from the current run list.
export default function Summary({ runs }) {
  const running = runs.filter((r) => r.status === "running" || r.status === "cancelling").length;
  const completed = runs.filter((r) => r.status === "completed").length;
  const failed = runs.filter((r) => r.status === "failed").length;

  const tiles = [
    { label: "Total runs", value: runs.length, cls: "i-accent", icon: <IconLayers size={20} /> },
    { label: "Running", value: running, cls: "i-run", icon: <IconPlay size={18} /> },
    { label: "Completed", value: completed, cls: "i-ok", icon: <IconCheck size={20} /> },
    { label: "Failed", value: failed, cls: "i-bad", icon: <IconX size={20} /> },
  ];

  return (
    <div className="summary">
      {tiles.map((t) => (
        <div className="tile" key={t.label}>
          <div className={`tile-icon ${t.cls}`}>{t.icon}</div>
          <div>
            <div className="tile-value">{t.value}</div>
            <div className="tile-label">{t.label}</div>
          </div>
        </div>
      ))}
    </div>
  );
}
