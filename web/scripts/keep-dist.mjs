import { writeFileSync } from 'node:fs'

writeFileSync(new URL('../dist/.gitkeep', import.meta.url), '')
