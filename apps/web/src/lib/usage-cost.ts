export function knownUsageCost(value: number | null | undefined): number | null {
  // The current usage contract stores missing prices and actual zero costs alike.
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : null
}
