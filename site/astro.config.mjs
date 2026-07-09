// @ts-check
import { defineConfig } from "astro/config";

import cloudflare from "@astrojs/cloudflare";

export default defineConfig({
  site: "https://www.getkastor.dev",
  output: "static",
  adapter: cloudflare(),
});