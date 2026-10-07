import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid,
  PieChart, Pie, Cell,
} from "recharts";
import { getRun, cancelRun } from "../api.js";
import StatusBadge from "../components/StatusBadge.jsx";
import { IconArrowLeft } from "../components/icons.jsx";

const TERMINAL = ["completed", "failed", "cancelled"];

const TOOLTIP_STYLE = {
  background: "#1b2230",
  border: "1px solid rgba(255,255,255,0.14)",
  borderRadius: 10,
  boxShadow: "0 16px 40px -12px rgba(0,0,0,0.6)",
  color: "#e6e9f0",
  fontSize: 12.5,
};
const AXIS = { fontSize: 12, fill: "#8b93a7" };
const SUCCESS = "#34d399";
const FAILED = "#f87171";
const OUTCOME_COLORS = [SUCCESS, FAILED];

export default function RunDetail() {
  const { id } = useParams();
  const [run, setRun] = useState(null);
  const [error, setError] = useState(null);

  // Poll the run every 2s, but only while it is not terminal (then stop).
  useEffect(() => {
    let active = true;
    let timer;
    async function load() {
      try {
        const data = await getRun(id);
        if (!active) return;
        setRun(data);
        setError(null);
        if (!TERMINAL.includes(data.status)) timer = setTimeout(load, 2000);
      } catch (err) {
        if (active) setError(err.message);
      }
    }
    load();
    return () => { active = false; clearTimeout(timer); };
  }, [id]);

  async function onCancel() {
    try { await cancelRun(id); } catch (err) { setError(err.message); }
  }

  if (error) {
    return (
      <div>
        <Link to="/" className="crumb"><IconArrowLeft /> Runs</Link>
        <div className="card"><div className="card-body"><p className="error">{error}</p></div></div>
      </div>
    );
  }
  if (!run) return <div className="card"><div className="card-body muted">Loading…</div></div>;

  const running = !TERMINAL.includes(run.status);
  const latency = [
    { name: "min", ms: run.minLatencyMs },
    { name: "p50", ms: run.p50LatencyMs },
    { name: "avg", ms: run.avgLatencyMs },
    { name: "p95", ms: run.p95LatencyMs },
    { name: "p99", ms: run.p99LatencyMs },
    { name: "max", ms: run.maxLatencyMs },
  ].filter((d) => d.ms != null);
  const outcome = [
    { name: "success", value: run.successfulRequests ?? 0 },
    { name: "failed", value: run.failedRequests ?? 0 },
  ];
  const hasOutcome = outcome.some((d) => d.value > 0);

  return (
    <div>
      <Link to="/" className="crumb"><IconArrowLeft /> Runs</Link>

      <div className="detail-head">
        <h2>Run #{run.id} · {run.name}</h2>
        <StatusBadge status={run.status} />
        {running && <button className="btn btn-danger" onClick={onCancel}>Cancel run</button>}
      </div>

      <div className="stat-row">
        <Stat label="Total requests" value={fmt(run.totalRequests)} />
        <Stat label="Successful" value={fmt(run.successfulRequests)} />
        <Stat label="Failed" value={fmt(run.failedRequests)} />
        <Stat label="p95 latency" value={run.p95LatencyMs != null ? `${run.p95LatencyMs} ms` : "-"} />
      </div>

      <div className="chart-row">
        <div className="card chart">
          <div className="card-head"><h2>Latency distribution</h2><p>Milliseconds across percentiles.</p></div>
          <div className="card-body">
            {latency.length ? (
              <ResponsiveContainer width="100%" height={230}>
                <BarChart data={latency} margin={{ top: 6, right: 6, left: -14, bottom: 0 }}>
                  <defs>
                    <linearGradient id="latGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#9a5cf0" />
                      <stop offset="100%" stopColor="#6a63e8" />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.07)" vertical={false} />
                  <XAxis dataKey="name" tick={AXIS} axisLine={false} tickLine={false} />
                  <YAxis tick={AXIS} axisLine={false} tickLine={false} width={46} />
                  <Tooltip contentStyle={TOOLTIP_STYLE} cursor={{ fill: "rgba(255,255,255,0.05)" }} />
                  <Bar dataKey="ms" fill="url(#latGrad)" radius={[6, 6, 0, 0]} maxBarSize={46} />
                </BarChart>
              </ResponsiveContainer>
            ) : (
              <p className="empty">{running ? "Waiting for results…" : "No latency samples."}</p>
            )}
          </div>
        </div>

        <div className="card chart">
          <div className="card-head"><h2>Request outcomes</h2><p>Successful vs failed requests.</p></div>
          <div className="card-body">
            {hasOutcome ? (
              <>
                <ResponsiveContainer width="100%" height={200}>
                  <PieChart>
                    <Pie data={outcome} dataKey="value" nameKey="name" innerRadius={54} outerRadius={84}
                      paddingAngle={2} stroke="#141a24" strokeWidth={3}>
                      {outcome.map((_, i) => <Cell key={i} fill={OUTCOME_COLORS[i]} />)}
                    </Pie>
                    <Tooltip contentStyle={TOOLTIP_STYLE} />
                  </PieChart>
                </ResponsiveContainer>
                <div className="pie-legend">
                  <span><i style={{ background: SUCCESS }} /> Success <b>{fmt(outcome[0].value)}</b></span>
                  <span><i style={{ background: FAILED }} /> Failed <b>{fmt(outcome[1].value)}</b></span>
                </div>
              </>
            ) : (
              <p className="empty">{running ? "Waiting for results…" : "No requests recorded."}</p>
            )}
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-head"><h2>Configuration</h2></div>
        <div className="card-body">
          <dl className="config">
            <div><dt>Target</dt><dd>{run.method} {run.targetUrl}</dd></div>
            <div><dt>Virtual users</dt><dd>{run.virtualUsers}</dd></div>
            <div><dt>Duration</dt><dd>{run.durationSeconds}s</dd></div>
            <div><dt>Shards</dt><dd>{run.shardCount}</dd></div>
          </dl>
          {run.errorMessage && <p className="muted" style={{ marginBottom: 0 }}>Note: {run.errorMessage}</p>}
        </div>
      </div>
    </div>
  );
}

function Stat({ label, value }) {
  return (
    <div className="card stat">
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  );
}

function fmt(n) {
  return n == null ? "-" : n.toLocaleString();
}
