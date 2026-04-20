export const DASHBOARD_DESKTOP_MEDIA_QUERY = '(min-width: 768px)';

export interface DashboardTrafficVisibilityState {
  hasVisibilitySupport: boolean;
  isDesktop: boolean;
  isVisible: boolean;
}

interface DashboardTrafficWindow {
  IntersectionObserver?: typeof IntersectionObserver;
  matchMedia?: typeof window.matchMedia;
}

export function hasDashboardTrafficVisibilitySupport(win?: DashboardTrafficWindow): boolean {
  return Boolean(win?.IntersectionObserver && win?.matchMedia);
}

export function isDashboardDesktopViewport(win?: DashboardTrafficWindow): boolean {
  return win?.matchMedia?.(DASHBOARD_DESKTOP_MEDIA_QUERY).matches ?? false;
}

export function canRenderDashboardTrafficSparkline({
  hasVisibilitySupport,
  isDesktop,
  isVisible,
}: DashboardTrafficVisibilityState): boolean {
  return hasVisibilitySupport && isDesktop && isVisible;
}
