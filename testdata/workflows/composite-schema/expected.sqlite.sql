-- Statement 1/3
CREATE TABLE "users" (
  "id" INTEGER PRIMARY KEY,
  "email" TEXT NOT NULL UNIQUE
);


-- Statement 2/3
CREATE TABLE "orders" (
  "id" INTEGER PRIMARY KEY,
  "user_id" INTEGER NOT NULL CONSTRAINT "fk_orders_user" REFERENCES "users" ("id"),
  "reference" TEXT NOT NULL
);


-- Statement 3/3
CREATE INDEX IF NOT EXISTS "idx_orders_reference" ON "orders" ("reference");
