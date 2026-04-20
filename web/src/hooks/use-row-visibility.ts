import { useEffect, useState, type RefCallback } from 'react';

import {
  DASHBOARD_DESKTOP_MEDIA_QUERY,
  hasDashboardTrafficVisibilitySupport,
  isDashboardDesktopViewport,
} from '@/lib/dashboard-traffic-visibility';

export interface RowVisibilityState {
  ref: RefCallback<HTMLTableRowElement>;
  hasVisibilitySupport: boolean;
  isDesktop: boolean;
  isVisible: boolean;
}

export type RowVisibilityHook = () => RowVisibilityState;

function getBrowserWindow(): Window | undefined {
  return typeof window === 'undefined' ? undefined : window;
}

export function useRowVisibility(): RowVisibilityState {
  const browserWindow = getBrowserWindow();
  const hasVisibilitySupport = hasDashboardTrafficVisibilitySupport(browserWindow);
  const [node, setNodeState] = useState<HTMLTableRowElement | null>(null);
  const [isDesktop, setIsDesktop] = useState(() => isDashboardDesktopViewport(browserWindow));
  const [isIntersecting, setIsIntersecting] = useState(false);

  const setNode: RefCallback<HTMLTableRowElement> = (nextNode) => {
    setIsIntersecting(false);
    setNodeState(nextNode);
  };

  useEffect(() => {
    if (!hasVisibilitySupport || !browserWindow?.matchMedia) {
      return;
    }

    const mediaQuery = browserWindow.matchMedia(DASHBOARD_DESKTOP_MEDIA_QUERY);
    const updateDesktop = () => {
      setIsDesktop(mediaQuery.matches);
      if (!mediaQuery.matches) {
        setIsIntersecting(false);
      }
    };

    updateDesktop();
    mediaQuery.addEventListener?.('change', updateDesktop);

    return () => {
      mediaQuery.removeEventListener?.('change', updateDesktop);
    };
  }, [browserWindow, hasVisibilitySupport]);

  useEffect(() => {
    if (!hasVisibilitySupport || !isDesktop || !node || typeof IntersectionObserver === 'undefined') {
      return;
    }

    const observer = new IntersectionObserver(
      (entries: IntersectionObserverEntry[]) => {
        setIsIntersecting(entries[0]?.isIntersecting ?? false);
      },
      { rootMargin: '160px 0px' },
    );

    observer.observe(node);

    return () => {
      observer.disconnect();
    };
  }, [hasVisibilitySupport, isDesktop, node]);

  return {
    ref: setNode,
    hasVisibilitySupport,
    isDesktop,
    isVisible: hasVisibilitySupport && isDesktop && isIntersecting,
  };
}
