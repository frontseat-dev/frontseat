import Database from 'better-sqlite3';

describe('the database the backend defaults to', () => {
  it('runs a query in memory', () => {
    const db = new Database(':memory:');
    expect(db.prepare('SELECT 1 AS one').get()).toEqual({ one: 1 });
    db.close();
  });
});
