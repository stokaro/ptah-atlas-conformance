-- Statement 1/4
CREATE TABLE "users" (
  "id" INTEGER PRIMARY KEY,
  "email" TEXT NOT NULL
);


-- Statement 2/4
CREATE TABLE "posts" (
  "id" INTEGER PRIMARY KEY,
  "user_id" INTEGER NOT NULL,
  "title" TEXT NOT NULL,
  CONSTRAINT "fk_posts_user" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON DELETE CASCADE
);


-- Statement 3/4
CREATE UNIQUE INDEX IF NOT EXISTS "idx_users_email" ON "users" ("email");


-- Statement 4/4
CREATE INDEX IF NOT EXISTS "idx_posts_user_id" ON "posts" ("user_id");
