const minimums = __MOBTERM_MINIMUMS_JSON__;
// Every capability minimum is a stable release. Validate prerelease identifiers
// before comparing the core, including on versions well above the minimum.
function atLeast(value, minimum) {
  if (typeof value !== "string" || value.length > 100 || value.trim() !== value) return false;
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(value);
  if (!match || match[4]?.split(".").some(id => /^0\d+$/.test(id))) return false;
  for (let i = 0; i < 3; i++) {
    const have = match[i + 1], need = String(minimum[i]);
    if (have.length !== need.length) return have.length > need.length;
    if (have !== need) return have > need;
  }
  return !match[4];
}
