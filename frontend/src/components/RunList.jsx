import { Link } from "react-router-dom";
import StatusBadge from "./StatusBadge.jsx";

// Presentational: Home owns the runs data (and its polling) and passes it in.
export default function RunList({ runs, error }) {
  return (
    <div className="card">
      <div className="card-head">
        <h2>Runs</h2>
        <p>Most recent test runs, updating live.</p>
      </div>
      {error && <div className="card-body"><p className="error">{error}</p></div>}
      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>Name</th>
            <th>Status</th>
            <th>VUs</th>
            <th>Shards</th>
            <th>Requests</th>
            <th>p95</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((r) => (
            <tr key={r.id}>
              <td><Link className="run-link" to={`/runs/${r.id}`}>#{r.id}</Link></td>
              <td>{r.name}</td>
              <td><StatusBadge status={r.status} /></td>
              <td className="num">{r.virtualUsers}</td>
              <td className="num">{r.shardCount}</td>
              <td className="num">{r.totalRequests != null ? r.totalRequests.toLocaleString() : "—"}</td>
              <td className="num">{r.p95LatencyMs != null ? `${r.p95LatencyMs} ms` : "—"}</td>
            </tr>
          ))}
          {runs.length === 0 && (
            <tr><td colSpan={7} className="empty">No runs yet — start one on the left.</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
