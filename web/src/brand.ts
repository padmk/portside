declare const __BRAND__: { name: string; storage: string }

export const brand = __BRAND__

export const storageKey = (name: string) => `${brand.storage}.${name}`
