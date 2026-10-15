export interface Match {
  score: number
  indices: number[]
}

/** Case-insensitive subsequence match that prefers consecutive runs and word starts. */
export function fuzzy(query: string, text: string): Match | null {
  const q = query.toLowerCase().replace(/\s+/g, '')
  if (!q) return { score: 0, indices: [] }
  const s = text.toLowerCase()
  const indices: number[] = []
  let score = 0
  let from = 0
  let prev = -2
  for (const ch of q) {
    const i = s.indexOf(ch, from)
    if (i < 0) return null
    const boundary = i === 0 || /[\s:/\-_.]/.test(s[i - 1] ?? '')
    score += 1 + (i === prev + 1 ? 4 : 0) + (boundary ? 3 : 0) - Math.min(i - from, 6) * 0.15
    indices.push(i)
    prev = i
    from = i + 1
  }
  if (s.startsWith(q)) score += 6
  return { score: score - s.length * 0.02, indices }
}
