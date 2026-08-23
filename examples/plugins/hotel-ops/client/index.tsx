// Hotel Operations Plugin — Client UI
// Registers custom tool-result cards for check_room_status and suggest_pricing.

const { React, definePlugin } = window.__PARSAR_PLUGIN_API__!
const { createElement: h } = React

// ─── Room Status Card ───────────────────────────────────────────────────────

interface RoomData {
  number: string
  type: string
  status: 'occupied' | 'vacant' | 'maintenance'
  guest: string | null
  checkout: string | null
}

interface RoomOverviewData {
  total: number
  vacant: number
  occupied: number
  maintenance: number
  rooms: RoomData[]
}

const STATUS_COLORS: Record<string, string> = {
  occupied: 'bg-blue-100 text-blue-800 border-blue-200',
  vacant: 'bg-green-100 text-green-800 border-green-200',
  maintenance: 'bg-amber-100 text-amber-800 border-amber-200',
}

const STATUS_LABELS: Record<string, string> = {
  occupied: '已入住',
  vacant: '空房',
  maintenance: '维护中',
}

function RoomBadge({ status }: { status: string }) {
  const colors = STATUS_COLORS[status] ?? 'bg-gray-100 text-gray-800 border-gray-200'
  const label = STATUS_LABELS[status] ?? status
  return h('span', {
    className: `inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${colors}`,
  }, label)
}

function RoomCard({ room }: { room: RoomData }) {
  return h('div', {
    className: 'rounded-lg border border-gray-200 bg-white p-3 shadow-sm',
  },
    h('div', { className: 'flex items-center justify-between mb-1.5' },
      h('span', { className: 'text-sm font-semibold text-gray-900' }, `#${room.number}`),
      h(RoomBadge, { status: room.status }),
    ),
    h('div', { className: 'text-xs text-gray-500' },
      h('span', { className: 'capitalize' }, room.type),
      room.guest && h('span', null, ` · ${room.guest}`),
      room.checkout && h('span', null, ` · 退房: ${room.checkout}`),
    ),
  )
}

function RoomStatusCard({ data }: { data: RoomOverviewData | RoomData }) {
  // Single room
  if ('number' in data && !('rooms' in data)) {
    return h('div', { className: 'my-2' },
      h(RoomCard, { room: data as RoomData }),
    )
  }

  // Room overview
  const overview = data as RoomOverviewData
  return h('div', { className: 'my-2 rounded-xl border border-gray-200 bg-gray-50 p-4' },
    // Summary bar
    h('div', { className: 'mb-3 flex items-center gap-3' },
      h('h3', { className: 'text-sm font-semibold text-gray-900' }, '房态概览'),
      h('div', { className: 'flex gap-2 text-xs' },
        h('span', { className: 'rounded-full bg-green-100 px-2 py-0.5 text-green-800' },
          `空房 ${overview.vacant}`),
        h('span', { className: 'rounded-full bg-blue-100 px-2 py-0.5 text-blue-800' },
          `已入住 ${overview.occupied}`),
        overview.maintenance > 0 &&
          h('span', { className: 'rounded-full bg-amber-100 px-2 py-0.5 text-amber-800' },
            `维护 ${overview.maintenance}`),
      ),
    ),
    // Occupancy progress bar
    h('div', { className: 'mb-3 h-2 w-full overflow-hidden rounded-full bg-gray-200' },
      h('div', {
        className: 'h-full rounded-full bg-blue-500 transition-all',
        style: { width: `${(overview.occupied / overview.total) * 100}%` },
      }),
    ),
    h('p', { className: 'mb-3 text-xs text-gray-500' },
      `入住率 ${Math.round((overview.occupied / overview.total) * 100)}% (${overview.occupied}/${overview.total})`),
    // Room grid
    h('div', { className: 'grid grid-cols-2 gap-2 sm:grid-cols-3' },
      ...overview.rooms.map((room) => h(RoomCard, { key: room.number, room })),
    ),
  )
}

// ─── Pricing Card ───────────────────────────────────────────────────────────

interface PricingData {
  room_type: string
  date: string
  base_price: number
  occupancy_rate: string
  multiplier: number
  suggested_price: number
  recommendation: string
}

const ROOM_TYPE_LABELS: Record<string, string> = {
  standard: '标准房',
  deluxe: '豪华房',
  suite: '套房',
}

function PricingCard({ data }: { data: PricingData }) {
  const isUp = data.multiplier > 1
  const isDown = data.multiplier < 1
  const arrowColor = isUp ? 'text-red-500' : isDown ? 'text-green-500' : 'text-gray-400'
  const arrow = isUp ? '↑' : isDown ? '↓' : '→'
  const bgColor = isUp ? 'bg-red-50 border-red-200' : isDown ? 'bg-green-50 border-green-200' : 'bg-gray-50 border-gray-200'
  const typeLabel = ROOM_TYPE_LABELS[data.room_type] ?? data.room_type

  return h('div', { className: `my-2 rounded-xl border p-4 ${bgColor}` },
    h('div', { className: 'flex items-center justify-between mb-3' },
      h('h3', { className: 'text-sm font-semibold text-gray-900' }, `${typeLabel} 定价建议`),
      h('span', { className: 'text-xs text-gray-500' }, data.date),
    ),
    // Price comparison
    h('div', { className: 'flex items-center gap-4 mb-3' },
      h('div', { className: 'text-center' },
        h('p', { className: 'text-xs text-gray-500' }, '基础价'),
        h('p', { className: 'text-lg font-semibold text-gray-700' }, `¥${data.base_price}`),
      ),
      h('span', { className: `text-2xl font-bold ${arrowColor}` }, arrow),
      h('div', { className: 'text-center' },
        h('p', { className: 'text-xs text-gray-500' }, '建议价'),
        h('p', { className: 'text-2xl font-bold text-gray-900' }, `¥${data.suggested_price}`),
      ),
    ),
    // Details
    h('div', { className: 'grid grid-cols-3 gap-2 rounded-lg bg-white/60 p-2 text-center text-xs' },
      h('div', null,
        h('p', { className: 'text-gray-500' }, '入住率'),
        h('p', { className: 'font-medium text-gray-900' }, data.occupancy_rate),
      ),
      h('div', null,
        h('p', { className: 'text-gray-500' }, '调价系数'),
        h('p', { className: 'font-medium text-gray-900' }, `${data.multiplier}x`),
      ),
      h('div', null,
        h('p', { className: 'text-gray-500' }, '建议'),
        h('p', { className: `font-medium ${isUp ? 'text-red-600' : isDown ? 'text-green-600' : 'text-gray-600'}` },
          isUp ? '上调' : isDown ? '下调' : '维持'),
      ),
    ),
  )
}

// ─── Hotel Operations Workspace ─────────────────────────────────────────────

// Mock data for the workspace dashboard (same as server mock).
const DASHBOARD_ROOMS: RoomData[] = [
  { number: '101', type: 'standard', status: 'occupied', guest: 'Zhang Wei', checkout: '2026-08-25' },
  { number: '102', type: 'standard', status: 'vacant', guest: null, checkout: null },
  { number: '201', type: 'deluxe', status: 'occupied', guest: 'Li Ming', checkout: '2026-08-24' },
  { number: '202', type: 'deluxe', status: 'maintenance', guest: null, checkout: null },
  { number: '301', type: 'suite', status: 'vacant', guest: null, checkout: null },
  { number: '302', type: 'suite', status: 'occupied', guest: 'Wang Fang', checkout: '2026-08-26' },
]

function HotelWorkspace() {
  const total = DASHBOARD_ROOMS.length
  const occupied = DASHBOARD_ROOMS.filter(r => r.status === 'occupied').length
  const vacant = DASHBOARD_ROOMS.filter(r => r.status === 'vacant').length
  const maintenance = DASHBOARD_ROOMS.filter(r => r.status === 'maintenance').length
  const occupancyRate = Math.round((occupied / total) * 100)
  const todayCheckouts = DASHBOARD_ROOMS.filter(r => r.checkout === '2026-08-23')

  return h('div', { className: 'flex h-full min-h-0 flex-1 flex-col bg-gray-50' },
    // ─── Top Stats Bar ───
    h('div', { className: 'border-b border-gray-200 bg-white px-6 py-4' },
      h('div', { className: 'flex items-center justify-between' },
        h('div', null,
          h('h1', { className: 'text-lg font-bold text-gray-900' }, '🏨 酒店运营工作台'),
          h('p', { className: 'text-sm text-gray-500' }, '实时房态监控 · 智能定价 · AI 运营助手'),
        ),
        h('div', { className: 'flex items-center gap-2' },
          h('span', { className: 'rounded-full bg-green-100 px-3 py-1 text-sm font-medium text-green-800' }, '系统正常'),
          h('span', { className: 'text-xs text-gray-400' }, '数据更新于 刚刚'),
        ),
      ),
    ),

    // ─── KPI Cards Row ───
    h('div', { className: 'grid grid-cols-4 gap-4 px-6 py-4' },
      h(KPICard, { label: '总房间数', value: String(total), icon: '🏠', color: 'gray' }),
      h(KPICard, { label: '入住率', value: `${occupancyRate}%`, icon: '📊', color: 'blue' }),
      h(KPICard, { label: '空房数', value: String(vacant), icon: '🔑', color: 'green' }),
      h(KPICard, { label: '维护中', value: String(maintenance), icon: '🔧', color: 'amber' }),
    ),

    // ─── Main Content: Room Grid + Sidebar ───
    h('div', { className: 'flex min-h-0 flex-1 gap-4 px-6 pb-4' },
      // Left: Room Grid
      h('div', { className: 'flex-1 overflow-y-auto rounded-xl border border-gray-200 bg-white p-4' },
        h('div', { className: 'mb-3 flex items-center justify-between' },
          h('h2', { className: 'text-sm font-semibold text-gray-900' }, '房间状态'),
          h('div', { className: 'flex gap-2' },
            h('span', { className: 'rounded bg-blue-100 px-2 py-0.5 text-xs text-blue-700' }, `已入住 ${occupied}`),
            h('span', { className: 'rounded bg-green-100 px-2 py-0.5 text-xs text-green-700' }, `空房 ${vacant}`),
            h('span', { className: 'rounded bg-amber-100 px-2 py-0.5 text-xs text-amber-700' }, `维护 ${maintenance}`),
          ),
        ),
        h('div', { className: 'grid grid-cols-3 gap-3' },
          ...DASHBOARD_ROOMS.map(room => h(WorkspaceRoomTile, { key: room.number, room })),
        ),
      ),

      // Right: Quick Actions + Today's Events
      h('div', { className: 'flex w-72 flex-col gap-4' },
        // Today's events
        h('div', { className: 'rounded-xl border border-gray-200 bg-white p-4' },
          h('h3', { className: 'mb-2 text-sm font-semibold text-gray-900' }, '📋 今日事项'),
          h('div', { className: 'space-y-2' },
            h(EventItem, { icon: '🔑', text: '201 退房 - Li Ming (预计今天)', urgent: true }),
            h(EventItem, { icon: '🧹', text: '202 维护完成确认', urgent: false }),
            h(EventItem, { icon: '💰', text: '周末定价策略待确认', urgent: false }),
          ),
        ),
        // Quick actions
        h('div', { className: 'rounded-xl border border-gray-200 bg-white p-4' },
          h('h3', { className: 'mb-2 text-sm font-semibold text-gray-900' }, '⚡ 快捷操作'),
          h('div', { className: 'space-y-2' },
            h(QuickAction, { label: '查看空房', desc: '查询当前可用房间' }),
            h(QuickAction, { label: '定价建议', desc: '获取 AI 定价推荐' }),
            h(QuickAction, { label: '入住率报告', desc: '本周入住趋势分析' }),
          ),
        ),
        // AI assistant hint
        h('div', { className: 'rounded-xl border border-blue-100 bg-blue-50 p-4' },
          h('p', { className: 'text-xs text-blue-700' },
            '💡 提示: 在左侧对话窗口向 AI 助手提问，可以查询实时房态、获取定价建议、生成运营报告。'),
        ),
      ),
    ),
  )
}

function KPICard({ label, value, icon, color }: { label: string; value: string; icon: string; color: string }) {
  const bgColors: Record<string, string> = {
    gray: 'bg-gray-50 border-gray-200',
    blue: 'bg-blue-50 border-blue-200',
    green: 'bg-green-50 border-green-200',
    amber: 'bg-amber-50 border-amber-200',
  }
  return h('div', { className: `rounded-xl border p-4 ${bgColors[color] ?? bgColors.gray}` },
    h('div', { className: 'flex items-center gap-2' },
      h('span', { className: 'text-2xl' }, icon),
      h('div', null,
        h('p', { className: 'text-xs text-gray-500' }, label),
        h('p', { className: 'text-xl font-bold text-gray-900' }, value),
      ),
    ),
  )
}

function WorkspaceRoomTile({ room }: { room: RoomData }) {
  const bgColors: Record<string, string> = {
    occupied: 'bg-blue-50 border-blue-200 hover:bg-blue-100',
    vacant: 'bg-green-50 border-green-200 hover:bg-green-100',
    maintenance: 'bg-amber-50 border-amber-200 hover:bg-amber-100',
  }
  const bg = bgColors[room.status] ?? 'bg-gray-50 border-gray-200'
  return h('div', { className: `cursor-pointer rounded-lg border p-3 transition-colors ${bg}` },
    h('div', { className: 'flex items-center justify-between' },
      h('span', { className: 'text-base font-bold text-gray-900' }, room.number),
      h(RoomBadge, { status: room.status }),
    ),
    h('p', { className: 'mt-1 text-xs text-gray-500 capitalize' }, room.type),
    room.guest && h('p', { className: 'mt-0.5 text-xs text-gray-700' }, `👤 ${room.guest}`),
    room.checkout && h('p', { className: 'mt-0.5 text-xs text-gray-400' }, `退房: ${room.checkout}`),
  )
}

function EventItem({ icon, text, urgent }: { icon: string; text: string; urgent: boolean }) {
  return h('div', { className: `flex items-start gap-2 rounded-lg p-2 text-sm ${urgent ? 'bg-red-50' : 'bg-gray-50'}` },
    h('span', null, icon),
    h('span', { className: urgent ? 'text-red-700 font-medium' : 'text-gray-700' }, text),
  )
}

function QuickAction({ label, desc }: { label: string; desc: string }) {
  return h('button', {
    type: 'button',
    className: 'w-full rounded-lg border border-gray-200 bg-gray-50 p-2 text-left transition-colors hover:bg-gray-100',
  },
    h('p', { className: 'text-sm font-medium text-gray-900' }, label),
    h('p', { className: 'text-xs text-gray-500' }, desc),
  )
}

// ─── Registration ───────────────────────────────────────────────────────────

definePlugin('@internal/hotel-ops', (ctx) => {
  // Register the full workspace replacement
  ctx.slots.register('workspace.main', {
    key: 'hotel-workspace',
    component: HotelWorkspace,
  })

  // Register room status card for tool results
  ctx.slots.register('conversation.tool-card', {
    key: 'room-status-card',
    match: (props) => {
      if (props.presentation?.kind === 'room-status-card') {
        return props.presentation.data
      }
      if (props.presentation?.kind === 'room-overview') {
        return props.presentation.data
      }
      return null
    },
    component: RoomStatusCard,
  })

  // Register pricing card for tool results
  ctx.slots.register('conversation.tool-card', {
    key: 'pricing-card',
    match: (props) => {
      if (props.presentation?.kind === 'pricing-suggestion') {
        return props.presentation.data
      }
      return null
    },
    component: PricingCard,
  })
})
