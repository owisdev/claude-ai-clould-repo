import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Built into the Go binary (internal/adminui/dist) and served under /admin/.
// `npm run dev` proxies the API to a server on localhost:3002.
export default defineConfig({
  base: "/admin/",
  plugins: [react()],
  build: {
    outDir: "../../internal/adminui/dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    proxy: {
      "/admin/v1": "http://localhost:3002",
      "/admin/config.json": "http://localhost:3002",
      "/api": "http://localhost:3002",
    },
  },
});
