// Hotel Operations Plugin — server tools
// Demonstrates Phase 1 plugin server capabilities: ctx.tools.define

// Mock PMS data (in production this would call the hotel's PMS API).
const MOCK_ROOMS = {
  '101': { number: '101', type: 'standard', status: 'occupied', guest: 'Zhang Wei', checkout: '2026-08-25' },
  '102': { number: '102', type: 'standard', status: 'vacant', guest: null, checkout: null },
  '201': { number: '201', type: 'deluxe', status: 'occupied', guest: 'Li Ming', checkout: '2026-08-24' },
  '202': { number: '202', type: 'deluxe', status: 'maintenance', guest: null, checkout: null },
  '301': { number: '301', type: 'suite', status: 'vacant', guest: null, checkout: null },
  '302': { number: '302', type: 'suite', status: 'occupied', guest: 'Wang Fang', checkout: '2026-08-26' },
};

const BASE_PRICES = {
  standard: 388,
  deluxe: 588,
  suite: 988,
};

export default (ctx) => {
  ctx.tools.define('check_room_status', {
    description: 'Check the current status of hotel rooms. Can query a specific room or get an overview of all rooms.',
    parameters: {
      type: 'object',
      properties: {
        room_number: {
          type: 'string',
          description: 'Specific room number to check (e.g. "101"). Omit to get all rooms.',
        },
        filter: {
          type: 'string',
          enum: ['all', 'vacant', 'occupied', 'maintenance'],
          description: 'Filter rooms by status. Defaults to "all".',
        },
      },
    },
    handler: async (args) => {
      const { room_number, filter = 'all' } = args;

      if (room_number) {
        const room = MOCK_ROOMS[room_number];
        if (!room) {
          return { value: { error: `Room ${room_number} not found` } };
        }
        return {
          value: room,
          presentation: { kind: 'room-status-card', data: room },
        };
      }

      // Return filtered room list.
      let rooms = Object.values(MOCK_ROOMS);
      if (filter !== 'all') {
        rooms = rooms.filter((r) => r.status === filter);
      }

      const summary = {
        total: Object.keys(MOCK_ROOMS).length,
        vacant: Object.values(MOCK_ROOMS).filter((r) => r.status === 'vacant').length,
        occupied: Object.values(MOCK_ROOMS).filter((r) => r.status === 'occupied').length,
        maintenance: Object.values(MOCK_ROOMS).filter((r) => r.status === 'maintenance').length,
        rooms,
      };

      return {
        value: summary,
        presentation: { kind: 'room-overview', data: summary },
      };
    },
  });

  ctx.tools.define('suggest_pricing', {
    description: 'Suggest room pricing adjustments based on current occupancy rate and room type.',
    parameters: {
      type: 'object',
      properties: {
        room_type: {
          type: 'string',
          enum: ['standard', 'deluxe', 'suite'],
          description: 'Room type to get pricing suggestion for.',
        },
        date: {
          type: 'string',
          description: 'Target date (YYYY-MM-DD). Defaults to today.',
        },
      },
      required: ['room_type'],
    },
    handler: async (args) => {
      const { room_type, date } = args;
      const basePrice = BASE_PRICES[room_type];
      if (!basePrice) {
        return { value: { error: `Unknown room type: ${room_type}` } };
      }

      // Simple occupancy-based pricing: high occupancy → higher price.
      const totalRooms = Object.values(MOCK_ROOMS).filter((r) => r.type === room_type).length;
      const occupiedRooms = Object.values(MOCK_ROOMS).filter(
        (r) => r.type === room_type && r.status === 'occupied'
      ).length;
      const occupancyRate = totalRooms > 0 ? occupiedRooms / totalRooms : 0;

      let multiplier = 1.0;
      if (occupancyRate >= 0.8) multiplier = 1.3;
      else if (occupancyRate >= 0.5) multiplier = 1.1;
      else if (occupancyRate < 0.3) multiplier = 0.85;

      const suggested = Math.round(basePrice * multiplier);

      return {
        value: {
          room_type,
          date: date || new Date().toISOString().slice(0, 10),
          base_price: basePrice,
          occupancy_rate: `${Math.round(occupancyRate * 100)}%`,
          multiplier,
          suggested_price: suggested,
          recommendation:
            multiplier > 1 ? 'Raise price (high demand)' : multiplier < 1 ? 'Lower price (low occupancy)' : 'Keep current price',
        },
      };
    },
  });
};
