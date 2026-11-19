- `syspi bundle` without `-index` now indexes the names and short names of every declaration at
  the root of a `.sysml` or `.kerml` file (KerML 1.1 Table 13, Note 1), not only the names of
  packages that start a line: short names (`package <v> Vehicle`), KerML namespaces, definitions,
  usages, types and features at the root, several declarations on one line, and a declaration
  after a closing brace. Braces and keywords inside notes, comments, string values and quoted
  names no longer mislead it, and escape sequences in a quoted name are resolved.
