import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  if (mode === "production" && !env.VITE_GAMEJAM_API_URL) {
    throw new Error("VITE_GAMEJAM_API_URL is required for a production build");
  }

  return {
    base: "./",
    optimizeDeps: {
      include: ["react", "react-dom/client"],
    },
    server: {
      warmup: {
        clientFiles: ["./src/main.jsx"],
      },
    },
    plugins: [react()],
  };
});
