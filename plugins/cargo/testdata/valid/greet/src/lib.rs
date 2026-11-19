/// A greeting for the nth caller.
pub fn greeting(n: u32) -> String {
    let mut buffer = itoa::Buffer::new();
    format!("hello, caller {}", buffer.format(n))
}
