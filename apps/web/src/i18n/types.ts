// TypeScript augmentation for i18next: makes `t('admin.models.page.title')`
// statically checked against the actual JSON shapes.
import "i18next"
import type { resources, defaultNS } from "./index"

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: typeof defaultNS
    resources: (typeof resources)["zh-CN"]
  }
}
