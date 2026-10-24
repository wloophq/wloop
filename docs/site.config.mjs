// The product's names, the only place the site spells them. Pages write
// {{name}}, {{command}}, {{app}}, {{org}} and {{repo}}; remark-product.mjs
// replaces them in text, code and links at build time.
export const product = {
  name: 'wloop',
  command: 'wloop',
  app: 'wloop',
  org: 'wloophq',
};

product.repo = `${product.org}/${product.name}`;
