import { defineI18n } from "fumadocs-core/i18n"

export const i18n = defineI18n({
  languages: ["en", "zh"],
  defaultLanguage: "en",
  hideLocale: "default-locale",
  parser: "dot",
})

export type Lang = (typeof i18n.languages)[number]
