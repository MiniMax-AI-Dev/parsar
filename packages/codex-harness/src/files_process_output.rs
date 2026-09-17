use codex_exec_server::{ExecOutputStream, ReadResponse};

const MAX_OUTPUT: usize = 4 * 1024 * 1024;

#[derive(Default)]
pub(super) struct Output {
    pub bytes: Vec<u8>,
    cursor: u64,
    exited: bool,
    closed: bool,
}

impl Output {
    pub fn after(&self) -> u64 {
        self.cursor
    }

    pub fn append(&mut self, response: &ReadResponse) -> Result<(), ()> {
        let next = response.next_seq.checked_sub(1).ok_or(())?;
        if next < self.cursor
            || (self.exited && !response.exited)
            || (self.closed && !response.closed)
            || response.exited != response.exit_code.is_some()
            || (response.closed && !response.exited)
        {
            return Err(());
        }
        let events = response.chunks.len() as u64
            + u64::from(response.exited && !self.exited)
            + u64::from(response.closed && !self.closed);
        if next - self.cursor != events {
            return Err(());
        }
        let mut previous = self.cursor;
        for chunk in &response.chunks {
            if chunk.seq <= previous
                || chunk.seq > next
                || chunk.stream != ExecOutputStream::Stdout
                || chunk.chunk.0.len() > MAX_OUTPUT.saturating_sub(self.bytes.len())
            {
                return Err(());
            }
            previous = chunk.seq;
            self.bytes.extend_from_slice(&chunk.chunk.0);
        }
        self.cursor = next;
        self.exited = response.exited;
        self.closed = response.closed;
        Ok(())
    }
}
