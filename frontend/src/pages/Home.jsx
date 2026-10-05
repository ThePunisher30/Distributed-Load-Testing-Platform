import { useCallback, useEffect, useState } from "react";
import CreateForm from "../components/CreateForm.jsx";
import RunList from "../components/RunList.jsx";
import Summary from "../components/Summary.jsx";
import { listRuns } from "../api.js";

export default function Home() {
  const [runs, setRuns] = useState([]);
  const [error, setError] = useState(null);

  // Home owns the runs data so the summary tiles and the list render from one
  // source. It loads on mount and polls every 3s for live updates.
  const load = useCallback(async () => {
    try {
      const data = await listRuns(50);
      setRuns(data);
      setError(null);
    } catch (err) {
      setError(err.message);
    }
  }, []);

  useEffect(() => {
    load();
    const timer = setInterval(load, 3000);
    return () => clearInterval(timer);
  }, [load]);

  return (
    <>
      <Summary runs={runs} />
      <div className="home">
        <CreateForm onCreated={load} />
        <RunList runs={runs} error={error} />
      </div>
    </>
  );
}
