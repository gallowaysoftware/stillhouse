import { useSyncExternalStore } from "react";

export type Lang = "en" | "fr";

const STORAGE_KEY = "stillhouse_lang";
const listeners = new Set<() => void>();

function readLang(): Lang {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return v === "fr" ? "fr" : "en";
  } catch {
    return "en";
  }
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function useLang(): Lang {
  return useSyncExternalStore(subscribe, readLang, () => "en");
}

export function setLang(next: Lang) {
  try {
    localStorage.setItem(STORAGE_KEY, next);
  } catch {
    // ignore quota / disabled storage
  }
  listeners.forEach((cb) => cb());
}

/**
 * t returns the appropriate-language string for a given pair.
 *
 *   t("Materials", "Matières premières")
 *
 * Callers without a French translation pass only the English; we fall
 * back to English for untranslated strings so the UI stays usable while
 * translations are filled in incrementally. Bill 96 compliance for QC
 * customers requires actually populating the French side for everything
 * a Quebec user would see — track those as TODOs as you find them.
 */
export function t(en: string, fr?: string): string {
  if (readLang() === "fr" && fr) return fr;
  return en;
}

// Hook flavour for components that re-render when the user toggles
// language. Returns a t() bound to the current lang.
export function useT(): (en: string, fr?: string) => string {
  const lang = useLang();
  return (en, fr) => (lang === "fr" && fr ? fr : en);
}
