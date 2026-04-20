import { describe, expect, test } from 'bun:test';

import {
  canRenderDashboardTrafficSparkline,
  hasDashboardTrafficVisibilitySupport,
  isDashboardDesktopViewport,
} from './dashboard-traffic-visibility';

describe('dashboard traffic visibility helpers', () => {
  test('only enables sparklines for visible desktop rows with support', () => {
    expect(canRenderDashboardTrafficSparkline({
      hasVisibilitySupport: true,
      isDesktop: true,
      isVisible: true,
    })).toBe(true);

    expect(canRenderDashboardTrafficSparkline({
      hasVisibilitySupport: true,
      isDesktop: true,
      isVisible: false,
    })).toBe(false);

    expect(canRenderDashboardTrafficSparkline({
      hasVisibilitySupport: false,
      isDesktop: true,
      isVisible: true,
    })).toBe(false);

    expect(canRenderDashboardTrafficSparkline({
      hasVisibilitySupport: true,
      isDesktop: false,
      isVisible: true,
    })).toBe(false);
  });

  test('treats missing browser capabilities as unsupported and non-desktop', () => {
    expect(hasDashboardTrafficVisibilitySupport()).toBe(false);
    expect(isDashboardDesktopViewport()).toBe(false);
  });
});
