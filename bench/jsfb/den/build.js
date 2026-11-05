// Compiles src/*.den into dist/app.js with the den compiler.
//
// Den is a Go program, so this needs Go 1.24 or later on PATH. By default Go
// fetches den from its module proxy at the pinned version below. Set
// DEN_REPO to a checkout of github.com/TriadStudios/Den to build with that
// checkout's compiler instead.
import { execFileSync } from "node:child_process";
import { rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const DEN = "github.com/TriadStudios/Den/cmd/den@v0.0.0-20261104135801-b8a90eaaf4bf";

const here = dirname(fileURLToPath(import.meta.url));
const src = join(here, "src");
const dist = join(here, "dist");
const repo = process.env.DEN_REPO;

rmSync(dist, { recursive: true, force: true });
execFileSync("go", ["run", repo ? "./cmd/den" : DEN, "build", src, "-o", dist], {
  cwd: repo || here,
  stdio: "inherit",
});
// den build also writes a page of its own; the benchmark serves ../index.html.
rmSync(join(dist, "index.html"), { force: true });
