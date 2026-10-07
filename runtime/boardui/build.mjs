// 构建看板前端：TS → 一个静态 app.js，出到 internal/board/ui/（随后被 go:embed 打进二进制）。
//
// 为什么产物要落在 Go 包里：embed 只能嵌**自己包目录之下**的文件。
// 所以源码与产物分两处 —— 源码在这里（带 node_modules，不入库），
// 产物在 Go 包那侧（入库，现场编译 Go 二进制不需要 node）。

import { build } from "esbuild";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const outfile = path.resolve(here, "..", "internal", "board", "ui", "app.js");

const result = await build({
  entryPoints: [path.join(here, "src", "app.ts")],
  outfile,
  bundle: true,
  format: "esm",
  target: ["chrome100", "safari15", "firefox100"],
  minify: true,
  legalComments: "none",
  sourcemap: false,
  metafile: false,
  logLevel: "info",
});

if (result.errors.length > 0) {
  console.error("构建失败：", result.errors);
  process.exit(1);
}
console.log(`已生成 ${path.relative(here, outfile)}`);
