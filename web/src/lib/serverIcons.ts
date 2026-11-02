// The icons a server can show, drawn on a 12 by 12 pixel grid. Holes are cut
// with the even-odd rule. Icons with only upright straight edges are marked
// `crisp` and drawn with crispEdges. The names must match `Icons` in
// internal/peers/peers.go.
export const serverIcons = {
  tower: { label: 'Tower', d: 'M3 0h6v12H3zM4 2v1h4V2zM4 4v1h4V4zM5 8v2h2V8z', crisp: true },
  rack: { label: 'Rack', d: 'M0 0h12v5H0zM0 7h12v5H0zM2 2v1h1V2zM5 2v1h5V2zM2 9v1h1V9zM5 9v1h5V9z', crisp: true },
  laptop: { label: 'Laptop', d: 'M2 1h8v7H2zM0 9h12v2H0z', crisp: true },
  monitor: { label: 'Desktop', d: 'M0 1h12v7H0zM5 8h2v2H5zM3 10h6v1H3z', crisp: true },
  box: { label: 'Mini PC', d: 'M1 2h10v8H1zM3 7v1h1V7zM6 7v1h3V7z', crisp: true },
  chip: { label: 'Chip', d: 'M3 3h6v6H3zM4 1h1v2H4zM7 1h1v2H7zM4 9h1v2H4zM7 9h1v2H7zM1 4h2v1H1zM1 7h2v1H1zM9 4h2v1H9zM9 7h2v1H9z', crisp: true },
  house: { label: 'House', d: 'M6 0l6 6h-2v6H7V8H5v4H2V6H0z' },
  cloud: { label: 'Cloud', d: 'M3 11a3 3 0 0 1-.4-5.97A3.5 3.5 0 0 1 9.4 4.1 3.5 3.5 0 0 1 9 11z' },
} as const

export type ServerIconName = keyof typeof serverIcons
export const serverIconNames = Object.keys(serverIcons) as ServerIconName[]
