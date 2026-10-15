import { useMemo } from 'react'
import { brand } from '../brand'
import { getLocale, usePrefs, type Locale } from '../state/prefs'
import { en, pl, type Key, type Message } from './dictionary'

export type { Key } from './dictionary'
export type Vars = Record<string, string | number>

const dictionaries: Record<Locale, Record<Key, Message>> = { en, pl }
const rules: Record<Locale, Intl.PluralRules> = {
  en: new Intl.PluralRules('en'),
  pl: new Intl.PluralRules('pl'),
}

export function translate(locale: Locale, key: Key, vars?: Vars): string {
  const msg = dictionaries[locale][key] ?? en[key]
  let text: string
  if (typeof msg === 'string') {
    text = msg
  } else {
    const n = Number(vars?.n ?? 0)
    text = msg[rules[locale].select(n)] ?? msg.other
  }
  const all: Vars = { product: brand.name, ...vars }
  return text.replace(/\{(\w+)\}/g, (m, k: string) => (k in all ? String(all[k]) : m))
}

export type Translate = (key: Key, vars?: Vars) => string

export function useT(): Translate {
  const { locale } = usePrefs()
  return useMemo<Translate>(() => (key, vars) => translate(locale, key, vars), [locale])
}

/** For code that runs outside React render, such as event handlers. */
export const t: Translate = (key, vars) => translate(getLocale(), key, vars)

export const locales: { id: Locale; label: string }[] = [
  { id: 'en', label: 'English' },
  { id: 'pl', label: 'Polski' },
]
