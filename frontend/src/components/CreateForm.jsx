import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { createRun } from "../api.js";

const initial = {
  name: "demo",
  targetUrl: "http://target:8081/fast",
  method: "GET",
  virtualUsers: 20,
  durationSeconds: 10,
  shards: 2,
  thinkTimeMs: 0,
};

export default function CreateForm({ onCreated }) {
  const [form, setForm] = useState(initial);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();

  function set(field, value) {
    setForm((f) => ({ ...f, [field]: value }));
  }

  async function submit(e) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const run = await createRun({
        name: form.name,
        targetUrl: form.targetUrl,
        method: form.method,
        virtualUsers: Number(form.virtualUsers),
        durationSeconds: Number(form.durationSeconds),
        shards: Number(form.shards),
        thinkTimeMs: Number(form.thinkTimeMs),
      });
      if (onCreated) onCreated();
      navigate(`/runs/${run.id}`);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card">
      <div className="card-head">
        <h2>New test run</h2>
        <p>Configure and launch a load test.</p>
      </div>
      <form className="card-body create-form" onSubmit={submit}>
        <div className="field">
          <label>Name</label>
          <input value={form.name} onChange={(e) => set("name", e.target.value)} />
        </div>
        <div className="field">
          <label>Target URL</label>
          <input value={form.targetUrl} onChange={(e) => set("targetUrl", e.target.value)} />
        </div>
        <div className="field">
          <label>Method</label>
          <select value={form.method} onChange={(e) => set("method", e.target.value)}>
            {["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"].map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
        </div>
        <div className="row">
          <div className="field">
            <label>Virtual users</label>
            <input type="number" min="1" value={form.virtualUsers} onChange={(e) => set("virtualUsers", e.target.value)} />
          </div>
          <div className="field">
            <label>Duration (s)</label>
            <input type="number" min="1" value={form.durationSeconds} onChange={(e) => set("durationSeconds", e.target.value)} />
          </div>
        </div>
        <div className="row">
          <div className="field">
            <label>Shards</label>
            <input type="number" min="1" value={form.shards} onChange={(e) => set("shards", e.target.value)} />
          </div>
          <div className="field">
            <label>Think time (ms)</label>
            <input type="number" min="0" value={form.thinkTimeMs} onChange={(e) => set("thinkTimeMs", e.target.value)} />
          </div>
        </div>
        {error && <p className="error">{error}</p>}
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? "Starting…" : "Start run"}
        </button>
      </form>
    </div>
  );
}
