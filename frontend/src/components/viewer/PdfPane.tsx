import { useEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Document, Page, pdfjs } from 'react-pdf';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';

// pdf.js runs its worker as a real web worker; Vite exposes the asset
// URL so WKWebView never needs file:// access for the worker file.
pdfjs.GlobalWorkerOptions.workerSrc = workerUrl;

// Vertical gap between sheets and the host's horizontal inset.
const ROW_GAP = 10;
const HOST_PAD = 16;
const MIN_PAGE_WIDTH = 240;
const MAX_PAGE_WIDTH = 1100;

export function PdfPane({ dataUrl, name }: { dataUrl: string; name: string }) {
  const [pages, setPages] = useState(0);
  const [error, setError] = useState('');
  const hostRef = useRef<HTMLDivElement>(null);
  const [pageWidth, setPageWidth] = useState(720);
  // fallbackRatio sizes unmeasured pages until their own ratio lands.
  const [fallbackRatio, setFallbackRatio] = useState(1.414);
  // ratios keeps per-page aspect ratios: cover pages and rotated pages
  // legitimately differ from the rest of the document.
  const [ratios, setRatios] = useState<Record<number, number>>({});

  // Fit pages to both the panel width and height: PDF pages that carry
  // lots of blank space at the bottom look much worse at "fit width"
  // scale, so the viewer prefers the largest size that keeps one page
  // mostly visible on screen.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const measure = () => {
      const w = host.clientWidth - 2 * HOST_PAD;
      const h = host.clientHeight - 2 * HOST_PAD;
      if (w <= 0 || h <= 0) return;
      const fitWidth = Math.min(w, Math.round(h / fallbackRatio));
      setPageWidth(
        Math.min(MAX_PAGE_WIDTH, Math.max(MIN_PAGE_WIDTH, fitWidth)),
      );
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(host);
    return () => observer.disconnect();
  }, [fallbackRatio]);

  // One sheet plus the inter-row gap; virtualization only mounts the
  // pages near the viewport instead of rendering every canvas at once.
  const sheetHeight = (index: number) =>
    Math.max(1, Math.round(pageWidth * (ratios[index] ?? fallbackRatio))) +
    ROW_GAP;

  const rowVirtualizer = useVirtualizer({
    count: pages,
    getScrollElement: () => hostRef.current,
    estimateSize: sheetHeight,
    overscan: 3,
  });

  // Row sizes change once page ratios and the fitted width land; tell
  // the virtualizer so cached offsets do not go stale.
  useEffect(() => {
    rowVirtualizer.measure();
  }, [rowVirtualizer, pages, pageWidth, ratios, fallbackRatio]);

  const noteRatio = (index: number, ratio: number) => {
    if (!(ratio > 0)) return;
    if (index === 0) {
      setFallbackRatio((prev) =>
        Math.abs(prev - ratio) < 1e-6 ? prev : ratio,
      );
    }
    setRatios((prev) =>
      prev[index] !== undefined && Math.abs(prev[index] - ratio) < 1e-6
        ? prev
        : { ...prev, [index]: ratio },
    );
  };

  return (
    <div ref={hostRef} className="pdf-host h-full overflow-auto bg-[#3a3d45]">
      {error && <div className="py-8 text-xs text-err">{error}</div>}
      <Document
        file={dataUrl}
        onLoadSuccess={({ numPages }) => setPages(numPages)}
        onLoadError={(err) => setError(String(err))}
      >
        <div
          className="relative w-full"
          style={{ height: rowVirtualizer.getTotalSize() }}
        >
          {rowVirtualizer.getVirtualItems().map((item) => {
            const index = item.index;
            return (
              <div
                key={item.key}
                data-index={index}
                ref={rowVirtualizer.measureElement}
                className="absolute left-0 top-0 w-full"
                style={{ transform: `translateY(${item.start}px)` }}
              >
                <div
                  className="flex justify-center"
                  style={{
                    height: sheetHeight(index),
                    paddingTop: Math.floor(ROW_GAP / 2),
                  }}
                >
                  <Page
                    key={`${name}-${index + 1}`}
                    pageNumber={index + 1}
                    width={pageWidth}
                    className="react-pdf__Page"
                    renderTextLayer={false}
                    renderAnnotationLayer={false}
                    onLoadSuccess={(proxy) => {
                      const [, , pw, ph] = proxy.view;
                      if (pw > 0 && ph > 0) noteRatio(index, ph / pw);
                    }}
                  />
                </div>
              </div>
            );
          })}
        </div>
      </Document>
    </div>
  );
}
