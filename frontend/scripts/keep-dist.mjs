// Vite empties the output directory; recreate the placeholder so `go:embed all:dist` works on a fresh checkout.
import { writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const dist = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'internal', 'webui', 'dist')
writeFileSync(join(dist, '.gitkeep'), '')
