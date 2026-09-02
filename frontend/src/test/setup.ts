import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';
import '../i18n';

afterEach(() => {
  cleanup();
});

// jsdom stubs for browser APIs the components touch but jsdom lacks.
if (typeof window !== 'undefined') {
  if (!window.matchMedia) {
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  }

  if (!('ResizeObserver' in window)) {
    class ResizeObserverStub {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    Object.defineProperty(window, 'ResizeObserver', {
      writable: true,
      value: ResizeObserverStub,
    });
  }

  if (!Element.prototype.scrollTo) {
    Element.prototype.scrollTo = () => {};
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }

  // ProseMirror needs hit-testing and text-node geometry to position the
  // cursor; jsdom has neither. Returning empty geometry keeps TipTap
  // interactions deterministic in unit tests.
  if (!document.elementFromPoint) {
    document.elementFromPoint = () => null;
  }
  const emptyRectList = {
    length: 0,
    item: () => null,
    [Symbol.iterator]: () => ({
      next: () => ({ done: true, value: undefined }),
    }),
  } as unknown as DOMRectList;
  if (!Element.prototype.getClientRects) {
    Element.prototype.getClientRects = () => emptyRectList;
  }
  const textProto = Text.prototype as unknown as {
    getClientRects?: () => DOMRectList;
    getBoundingClientRect?: () => DOMRect;
  };
  if (!textProto.getClientRects) {
    textProto.getClientRects = () => emptyRectList;
  }
  if (!textProto.getBoundingClientRect) {
    textProto.getBoundingClientRect = () =>
      ({
        top: 0,
        right: 0,
        bottom: 0,
        left: 0,
        width: 0,
        height: 0,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      }) as DOMRect;
  }
  if (!Range.prototype.getClientRects) {
    Range.prototype.getClientRects = () => emptyRectList;
  }
  if (!Range.prototype.getBoundingClientRect) {
    Range.prototype.getBoundingClientRect = () =>
      ({
        top: 0,
        right: 0,
        bottom: 0,
        left: 0,
        width: 0,
        height: 0,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      }) as DOMRect;
  }
}
