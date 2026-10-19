import { cn } from "@/lib/utils";

// Bee is Hive's mascot, pixel for pixel the terminal UI's bee
// (internal/tui/bee) with its wings at rest: each terminal cell is two
// square pixels, so the art is 16 by 13.
export function Bee({ className, title }: { className?: string; title?: string }) {
  return (
    <svg viewBox="0 0 16 13" shapeRendering="crispEdges" className={cn("h-[1em] w-auto", className)} role={title ? "img" : undefined} aria-hidden={title ? undefined : true}>
      {title && <title>{title}</title>}
      <path fill="#BDBDBB" d="M0 2h2v1H0zM14 2h2v1h-2zM0 3h3v3H0zM13 3h3v3h-3z" />
      <path fill="#9B9BA1" d="M4 1h1v1H4zM11 1h1v1h-1zM5 2h1v1H5zM10 2h1v1h-1z" />
      <path
        fill="#F5B942"
        d="M3 0h1v1H3zM12 0h1v1h-1zM4 3h8v1H4zM3 4h10v1H3zM3 5h2v2H3zM6 5h4v2H6zM11 5h2v2h-2zM3 7h10v1H3zM3 8h1v1H3zM12 8h1v1h-1zM1 9h14v1H1zM4 10h1v1H4zM11 10h1v1h-1zM4 11h1v2H4zM6 11h1v2H6zM9 11h1v2H9zM11 11h1v2h-1z"
      />
      <path fill="#0B0B0C" d="M5 5h1v2H5zM10 5h1v2h-1zM4 8h8v1H4zM5 10h6v1H5z" />
    </svg>
  );
}
