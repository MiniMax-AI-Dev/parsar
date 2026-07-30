import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"

export type DirectorySort = "featured" | "name"

interface DirectoryFilters {
  query: string
  category: string
  verifiedOnly: boolean
  sort: DirectorySort
}

export function filterMCPDirectoryItems(items: MCPDirectoryItem[], filters: DirectoryFilters): MCPDirectoryItem[] {
  const needle = filters.query.trim().toLocaleLowerCase()
  const filtered = items.filter((item) => {
    if (filters.category && !item.categories.includes(filters.category)) return false
    if (filters.verifiedOnly && !item.verified) return false
    if (!needle) return true
    return [item.name, item.description, item.publisher.name, ...item.categories]
      .join(" ")
      .toLocaleLowerCase()
      .includes(needle)
  })
  return filtered.sort((left, right) => filters.sort === "name"
    ? left.name.localeCompare(right.name)
    : left.featured_rank - right.featured_rank || left.name.localeCompare(right.name))
}
