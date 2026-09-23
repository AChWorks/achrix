# Engineering Principles

These principles are guardrails, not excuses for ceremony.

1. **Reuse before build.**
2. **Start simple; evolve from evidence.**
3. **Keep Core minimal.**
4. **Modules own their domain and durable state.**
5. **Boundaries follow credible variation, not imagination.**
6. **Stable contracts; replaceable infrastructure.**
7. **A boundary does not imply a package/service/plugin/repository.**
8. **Use framework/native capabilities where fit; do not reimplement them merely for purity.**
9. **Avoid unnecessary framework coupling in business semantics where it materially harms reuse/testing/evolution.**
10. **Human and machine interfaces reuse the same Application capabilities.**
11. **Security and authorization are explicit and default-deny.**
12. **External effects have an explicit owner and failure model.**
13. **Compatibility is intentional; breaking public/durable contracts are versioned.**
14. **Performance and infrastructure are evidence-driven.**
15. **Data should remain recoverable and not be unnecessarily trapped.**
16. **Semantic representations improve SEO, accessibility, automation, and AI readability.**
17. **Important architecture constraints should be machine-checkable when recurring regressions justify it.**
18. **Prefer reversible decisions while uncertainty is high.**
19. **Duplication can be cheaper than a premature shared abstraction.**
20. **Delete architecture that has no current owner/value; do not preserve complexity for hypothetical futures.**
21. **Golden paths are optional paved roads; keep explicit, bounded escape hatches for justified product-specific needs.**
22. **Shared Foundation runtime should be consumed/versioned, not permanently copied into every product.**
23. **Design cheap credible reuse paths, but promote/extract only from real consumer evidence.**

## Practical decision sequence

For a new requirement:

```text
Can existing code/native/framework capability solve it?
        |
       yes -> reuse/configure
        |
       no
        v
Can a bounded extension/adapter solve it?
        |
       yes -> extend/adapt
        |
       no
        v
Is there repeated proven common behavior worth promoting?
        |
       yes -> promote to the smallest shared ownership level that fits
        |
       no
        v
Build the smallest local solution
```

## Proportionality

Do not add:

- interfaces;
- services;
- packages;
- events;
- queues;
- caches;
- metrics;
- ADRs;
- documentation;

merely because good systems sometimes contain them.

Add them when they materially improve correctness, ownership, compatibility, operability, recovery, or delivery.
