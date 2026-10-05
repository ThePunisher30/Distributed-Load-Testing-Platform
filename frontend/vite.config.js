import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The app calls the backend under /api. In production nginx reverse-proxies
// /api -> backend (same origin). For `npm run dev`, Vite proxies it the same way,
// stripping the /api prefix, so the frontend code is identical in both modes.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/api/, ""),
      },
    },
  },
});
