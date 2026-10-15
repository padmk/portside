declare module 'virtual:theme-swatches' {
  export interface Swatch {
    bg: string
    card: string
    border: string
    text: string
    primary: string
    'primary-fg': string
    running: string
    starting: string
    crashed: string
    external: string
  }
  export const swatches: Record<string, Swatch>
}
