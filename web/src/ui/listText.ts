/** A list edited as text, one item per line (or separated by `sep`), without empty items. */
export const listOf = (text: string, sep: RegExp = /\n/): string[] =>
  text
    .split(sep)
    .map((s) => s.trim())
    .filter(Boolean)

export const listText = (items: string[] | null | undefined, sep = '\n') => (items ?? []).join(sep)
