use crate::wire;
use prost::Message;

#[test]
fn corrupt_host_replies_are_errors_and_empty_replies_remain_valid() {
    for bytes in [vec![0xff], vec![0x12, 0x05, b'a']] {
        let error = crate::abi::decode_host_reply::<wire::ReadResponse>(&bytes).unwrap_err();
        assert_eq!(error.reason, "invalid-host-response");
        assert_eq!(error.code, crate::Code::Internal);
        assert!(crate::abi::decode_host_reply::<wire::GlobResponse>(&bytes).is_err());
    }
    let absent = crate::abi::decode_host_reply::<wire::ReadResponse>(&[]).unwrap();
    assert!(!absent.found);
    let empty = wire::ReadResponse {
        found: true,
        content: vec![],
    };
    assert_eq!(
        crate::abi::decode_host_reply::<wire::ReadResponse>(&empty.encode_to_vec()).unwrap(),
        empty
    );
    assert!(crate::abi::decode_host_reply::<wire::GlobResponse>(&[])
        .unwrap()
        .paths
        .is_empty());
}
