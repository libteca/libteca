import { describe, expect, it } from "vitest";
import renderToString from "preact-render-to-string";
import { LibraryWorks } from "../src/views/library";
import type { LibraryPage } from "../src/libraryPagination";

function page(overrides: Partial<LibraryPage> = {}): LibraryPage {
  return {
    works: Array.from({ length: 200 }, (_, index) => ({
      id: index + 1, title: `Work ${index + 1}`, author: null, subtitle: null, hasCover: false, editions: [],
    })),
    loaded: true, loading: false, hasMore: true, error: false, ...overrides,
  };
}

function render(state: LibraryPage) {
  return renderToString(<LibraryWorks page={state} type="books" filter="all" loadMore={() => {}} />);
}

describe("LibraryWorks pagination controls", () => {
  it("shows the loaded count and a load-more control without claiming a known total", () => {
    const html = render(page());
    expect(html.match(/class="cover-card"/g)).toHaveLength(200);
    expect(html).toContain("Showing 200 works");
    expect(html).toContain("Load more");
    expect(html).not.toContain("All loaded");
  });

  it("keeps cards and count visible with a retry control after a next-page failure", () => {
    const html = render(page({ error: true }));
    expect(html.match(/class="cover-card"/g)).toHaveLength(200);
    expect(html).toContain("Showing 200 works");
    expect(html).toContain('role="alert"');
    expect(html).toContain("Retry load more");
  });

  it("disables load more while a page is loading and keeps existing cards visible", () => {
    const html = render(page({ loading: true }));
    expect(html.match(/class="cover-card"/g)).toHaveLength(200);
    expect(html).toContain("disabled");
    expect(html).toContain("Loading…");
    expect(html).toContain('aria-busy="true"');
  });

  it("only shows all-loaded status once the server page establishes the end", () => {
    const html = render(page({ hasMore: false }));
    expect(html).toContain("Showing 200 works · All loaded");
    expect(html).not.toContain("Load more");
  });

  it("does not flash the empty-library state while retrying the first page", () => {
    const html = render(page({ works: [], loading: true }));
    expect(html).not.toContain("No works yet");
    expect(html).not.toContain("Showing 0");
    expect(html).toContain('class="sk"');
  });
});
