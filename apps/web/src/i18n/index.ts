import i18n from "i18next"
import LanguageDetector from "i18next-browser-languagedetector"
import { initReactI18next } from "react-i18next"

import enCommon from "./locales/en-US/common.json"
import enAdmin from "./locales/en-US/admin.json"
import zhCommon from "./locales/zh-CN/common.json"
import zhAdmin from "./locales/zh-CN/admin.json"

export const SUPPORTED_LANGUAGES = ["zh-CN", "en-US"] as const
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number]

export const resources = {
  "en-US": {
    common: enCommon,
    admin: enAdmin,
  },
  "zh-CN": {
    common: zhCommon,
    admin: zhAdmin,
  },
} as const

export const defaultNS = "common" as const

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: "zh-CN",
    supportedLngs: [...SUPPORTED_LANGUAGES],
    defaultNS,
    ns: ["common", "admin"],
    interpolation: {
      escapeValue: false, // react already escapes
    },
    detection: {
      order: ["localStorage", "navigator", "htmlTag"],
      caches: ["localStorage"],
      lookupLocalStorage: "parsar.lang",
    },
  })

export default i18n
