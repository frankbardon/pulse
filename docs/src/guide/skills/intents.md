```yaml
name: intents
description: The closed intent taxonomy: the kinds of question an analysis answers, how each sounds, and the field shapes it fits. Use to map a user's question to an intent ID.
type: reference
kind: reference
```

# Intents

The closed set of question kinds an analysis can answer. Operator purposes and examples cite these IDs; a shape lists the roles a cohort's fields must fill for the question to be answerable.

## Analytic intents

### benchmark

Benchmark against a reference.

Sounds like:
- How does this compare to the total?
- Is this group above or below the population?
- How does this wave compare to the last?

Shapes:
- measure (numeric|categorical|bool, 1 or more) + group (categorical|bool|date, exactly 1)

### change_over_time

Change over time.

Sounds like:
- Is it going up?
- How does this month compare to last?

Shapes:
- time (date, exactly 1) + measure (numeric|categorical|bool, 1 or more)

### compare_groups

Compare groups.

Sounds like:
- Do A and B differ?
- Is this segment different from the rest?

Shapes:
- outcome (numeric|categorical|bool, exactly 1) + group (categorical|bool, exactly 1)

### composition

Mix and share.

Sounds like:
- What's the mix?
- What share does each option have?
- Which attributes go with which brand?

Shapes:
- category (categorical|bool|set, exactly 1) + by (categorical|bool, optional)

### data_quality

Data quality.

Sounds like:
- Is this data trustworthy?
- Who answered carelessly?
- How much is missing?

Shapes:
- fields (numeric|categorical|date|bool|set, 1 or more)

### describe

Describe a measure.

Sounds like:
- What's the typical value of X?
- What's the total X?
- How spread out is X?

Shapes:
- measure (numeric, 1 or more)
- measure (categorical|bool|set, 1 or more)

### distribution_shape

Distribution shape.

Sounds like:
- Is it normally distributed?
- Are there outliers?
- Is it skewed?

Shapes:
- measure (numeric, 1 or more)

### drivers

Find drivers.

Sounds like:
- What explains Y?
- Which factors matter most for Y?

Shapes:
- outcome (numeric|categorical|bool, exactly 1) + predictors (numeric|categorical|bool, 1 or more)

### flows

Flows between states.

Sounds like:
- Where do customers move between states?
- Who switched from A to B?

Shapes:
- from (categorical|bool, exactly 1) + to (categorical|bool, exactly 1)

### measure_construct

Measure a construct.

Sounds like:
- Do these questions measure one thing?
- Combine these items into a score.

Shapes:
- items (numeric, 2 or more)

### relationship

Relationship between measures.

Sounds like:
- Do X and Y move together?
- Is X associated with Y?

Shapes:
- measures (numeric, 2 or more)
- categories (categorical|bool, exactly 2)

### segment

Segment into groups.

Sounds like:
- Are there natural groups of customers?
- Split this into tiers.

Shapes:
- measures (numeric, 1 or more)

## Tooling intents

### lookup

Look up records.

Sounds like:
- Show me the record for this ID.
- Fetch the rows for this key.

Shapes:
- key (numeric|categorical|date, 1 or more)

### prepare

Prepare data.

Sounds like:
- Keep only last year's rows.
- Derive a new column from these fields.

Shapes:
- fields (numeric|categorical|date|bool|set, 1 or more)

### simulate

Simulate data.

Sounds like:
- Generate a synthetic copy of this cohort.
- Make test data with this schema.

Shapes:
- fields (numeric|categorical|date|bool|set, 1 or more)
