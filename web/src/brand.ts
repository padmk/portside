declare const __BRAND__: { name: string; storage: string }

export const brand = __BRAND__

/** The product name as a title in UI copy; `brand.name` is also the command. */
export const brandTitle = brand.name.charAt(0).toUpperCase() + brand.name.slice(1)

export const storageKey = (name: string) => `${brand.storage}.${name}`
