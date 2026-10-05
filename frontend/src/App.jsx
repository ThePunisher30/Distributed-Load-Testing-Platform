import { Routes, Route, Link } from "react-router-dom";
import Home from "./pages/Home.jsx";
import RunDetail from "./pages/RunDetail.jsx";
import { IconBolt, IconExternal } from "./components/icons.jsx";

export default function App() {
  return (
    <div className="app">
      <header>
        <Link to="/" className="brand">
          <span className="logo-mark"><IconBolt size={18} /></span>
          <span className="brand-text">
            Load Testing
            <small>Distributed platform</small>
          </span>
        </Link>
        <a className="ghost-link" href="http://localhost:3000" target="_blank" rel="noreferrer">
          Live metrics <IconExternal />
        </a>
      </header>
      <main>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/runs/:id" element={<RunDetail />} />
        </Routes>
      </main>
    </div>
  );
}
