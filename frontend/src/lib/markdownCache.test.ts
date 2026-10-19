import { describe, expect, it } from 'vitest';
import { MarkdownCache } from './markdownCache';

// The cache charges a block its source text, so the tests work in whole
// "blocks" instead of markdown.
const block = (size: number, fill = 'a') => fill.repeat(size);

describe('MarkdownCache', () => {
  it('answers a tree it has parsed without parsing again', () => {
    const cache = new MarkdownCache();
    const tree = { type: 'p' } as never;
    expect(cache.get('# hello')).toBeUndefined();
    cache.set('# hello', tree);

    expect(cache.get('# hello')).toBe(tree);
    expect(cache.size).toBe(1);
    expect(cache.charCount).toBe('# hello'.length);
    // One miss (the first mount) and one hit (the second).
    expect(cache.misses).toBe(1);
    expect(cache.hits).toBe(1);
  });

  it('drops the oldest block once the budget is spent', () => {
    // Three blocks of ten characters, a budget of twenty-five: the third
    // parse pushes the first out.
    const cache = new MarkdownCache(25, 8);
    cache.set(block(10, 'a'), 'a' as never);
    cache.set(block(10, 'b'), 'b' as never);
    expect(cache.charCount).toBe(20);
    cache.set(block(10, 'c'), 'c' as never);

    expect(cache.get(block(10, 'a'))).toBeUndefined();
    expect(cache.get(block(10, 'b'))).toBe('b');
    expect(cache.get(block(10, 'c'))).toBe('c');
    expect(cache.charCount).toBe(20);
  });

  it('keeps what the reader is looking at, not what arrived last', () => {
    const cache = new MarkdownCache(25, 8);
    cache.set(block(10, 'a'), 'a' as never);
    cache.set(block(10, 'b'), 'b' as never);
    // Reading the older block makes it the newest use, so the next parse
    // evicts the block that arrived between them instead of this one.
    expect(cache.get(block(10, 'a'))).toBe('a');
    cache.set(block(10, 'c'), 'c' as never);

    expect(cache.get(block(10, 'a'))).toBe('a');
    expect(cache.get(block(10, 'b'))).toBeUndefined();
    expect(cache.get(block(10, 'c'))).toBe('c');
  });

  it('bounds the keys as well as the characters', () => {
    // Three one-character blocks fit the budget many times over; the
    // entry bound is what keeps a thousand one-line messages from trading
    // characters for bookkeeping.
    const cache = new MarkdownCache(1000, 2);
    cache.set('a', 'a' as never);
    cache.set('b', 'b' as never);
    cache.set('c', 'c' as never);

    expect(cache.size).toBe(2);
    expect(cache.get('a')).toBeUndefined();
    expect(cache.get('b')).toBe('b');
    expect(cache.get('c')).toBe('c');
  });

  it('does not keep a block past the entry size', () => {
    // The huge answer is rendered and left out: keeping it would evict
    // every block the reader had just scrolled over.
    const cache = new MarkdownCache(100, 8, 10);
    const huge = block(11);
    cache.set(huge, 'huge' as never);

    expect(cache.size).toBe(0);
    expect(cache.charCount).toBe(0);
    expect(cache.get(huge)).toBeUndefined();
  });

  it('clear drops every tree and its accounting', () => {
    const cache = new MarkdownCache();
    cache.set('# one', 'one' as never);
    cache.set('# two', 'two' as never);

    cache.clear();

    expect(cache.size).toBe(0);
    expect(cache.charCount).toBe(0);
    expect(cache.get('# one')).toBeUndefined();
  });
});
